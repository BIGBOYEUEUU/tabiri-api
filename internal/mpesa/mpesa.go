package mpesa

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/tabiri/api/config"
	"github.com/tabiri/api/internal/middleware"
	"github.com/tabiri/api/internal/models"
	"github.com/tabiri/api/internal/wallet"
)

type Service struct {
	db        *sqlx.DB
	walletSvc *wallet.Service
	cfg       *config.Config
}

func NewService(db *sqlx.DB, walletSvc *wallet.Service, cfg *config.Config) *Service {
	return &Service{db: db, walletSvc: walletSvc, cfg: cfg}
}

// baseURL returns the Safaricom Daraja API base URL.
func (s *Service) baseURL() string {
	if s.cfg.MpesaEnv == "production" {
		return "https://api.safaricom.co.ke"
	}
	return "https://sandbox.safaricom.co.ke"
}

// ── OAuth Token ───────────────────────────────────────────────

type oauthResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   string `json:"expires_in"`
}

func (s *Service) getAccessToken() (string, error) {
	creds := base64.StdEncoding.EncodeToString(
		[]byte(s.cfg.MpesaConsumerKey + ":" + s.cfg.MpesaConsumerSecret),
	)

	req, _ := http.NewRequest("GET",
		s.baseURL()+"/oauth/v1/generate?grant_type=client_credentials", nil)
	req.Header.Set("Authorization", "Basic "+creds)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth request: %w", err)
	}
	defer resp.Body.Close()

	var result oauthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode oauth: %w", err)
	}
	return result.AccessToken, nil
}

// ── STK Push (Deposit) ────────────────────────────────────────

type stkPushRequest struct {
	BusinessShortCode string `json:"BusinessShortCode"`
	Password          string `json:"Password"`
	Timestamp         string `json:"Timestamp"`
	TransactionType   string `json:"TransactionType"`
	Amount            int64  `json:"Amount"`
	PartyA            string `json:"PartyA"`   // customer phone
	PartyB            string `json:"PartyB"`   // shortcode
	PhoneNumber       string `json:"PhoneNumber"`
	CallBackURL       string `json:"CallBackURL"`
	AccountReference  string `json:"AccountReference"`
	TransactionDesc   string `json:"TransactionDesc"`
}

type stkPushResponse struct {
	MerchantRequestID string `json:"MerchantRequestID"`
	CheckoutRequestID string `json:"CheckoutRequestID"`
	ResponseCode      string `json:"ResponseCode"`
	ResponseDesc      string `json:"ResponseDescription"`
	CustomerMessage   string `json:"CustomerMessage"`
}

// InitiateDeposit sends an STK Push to the customer's phone.
func (s *Service) InitiateDeposit(userID uuid.UUID, amountKES float64, phone string) (*models.MpesaRequest, error) {
	token, err := s.getAccessToken()
	if err != nil {
		return nil, err
	}

	ts := time.Now().Format("20060102150405")
	password := base64.StdEncoding.EncodeToString(
		[]byte(s.cfg.MpesaShortcode + s.cfg.MpesaPasskey + ts),
	)

	payload := stkPushRequest{
		BusinessShortCode: s.cfg.MpesaShortcode,
		Password:          password,
		Timestamp:         ts,
		TransactionType:   "CustomerPayBillOnline",
		Amount:            int64(amountKES),
		PartyA:            phone,
		PartyB:            s.cfg.MpesaShortcode,
		PhoneNumber:       phone,
		CallBackURL:       s.cfg.MpesaCallbackURL,
		AccountReference:  "Tabiri",
		TransactionDesc:   "Tabiri wallet deposit",
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST",
		s.baseURL()+"/mpesa/stkpush/v1/processrequest",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stk push request: %w", err)
	}
	defer resp.Body.Close()

	var result stkPushResponse
	respBody, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode stk response: %w", err)
	}

	if result.ResponseCode != "0" {
		return nil, fmt.Errorf("stk push failed: %s", result.ResponseDesc)
	}

	// Store the pending request
	amountKobo := int64(amountKES * 100)
	var mpesaReq models.MpesaRequest
	err = s.db.QueryRowx(`
		INSERT INTO mpesa_requests
		    (user_id, type, merchant_request_id, checkout_request_id, amount_kobo, phone, status)
		VALUES ($1, 'stk_push', $2, $3, $4, $5, 'pending')
		RETURNING *
	`, userID, result.MerchantRequestID, result.CheckoutRequestID, amountKobo, phone).
		StructScan(&mpesaReq)

	return &mpesaReq, err
}

// ── STK Callback ─────────────────────────────────────────────

type stkCallback struct {
	Body struct {
		STKCallback struct {
			MerchantRequestID string `json:"MerchantRequestID"`
			CheckoutRequestID string `json:"CheckoutRequestID"`
			ResultCode        int    `json:"ResultCode"`
			ResultDesc        string `json:"ResultDesc"`
			CallbackMetadata  *struct {
				Item []struct {
					Name  string      `json:"Name"`
					Value interface{} `json:"Value"`
				} `json:"Item"`
			} `json:"CallbackMetadata"`
		} `json:"stkCallback"`
	} `json:"Body"`
}

// HandleDepositCallback processes the M-Pesa STK callback.
func (s *Service) HandleDepositCallback(c *gin.Context) {
	var cb stkCallback
	if err := c.ShouldBindJSON(&cb); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid callback"})
		return
	}

	stk := cb.Body.STKCallback
	resultCode := fmt.Sprintf("%d", stk.ResultCode)
	resultDesc := stk.ResultDesc

	// Find the pending request
	var mpesaReq models.MpesaRequest
	err := s.db.Get(&mpesaReq, `
		SELECT * FROM mpesa_requests
		WHERE checkout_request_id = $1 AND status = 'pending'
	`, stk.CheckoutRequestID)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ResultCode": 0, "ResultDesc": "Accepted"})
		return
	}

	if stk.ResultCode != 0 {
		// Payment failed or cancelled
		s.db.Exec(`
			UPDATE mpesa_requests
			SET status = 'failed', result_code = $1, result_desc = $2, completed_at = NOW()
			WHERE id = $3
		`, resultCode, resultDesc, mpesaReq.ID)
		c.JSON(http.StatusOK, gin.H{"ResultCode": 0, "ResultDesc": "Accepted"})
		return
	}

	// Extract M-Pesa receipt from metadata
	var receipt string
	if stk.CallbackMetadata != nil {
		for _, item := range stk.CallbackMetadata.Item {
			if item.Name == "MpesaReceiptNumber" {
				receipt = fmt.Sprintf("%v", item.Value)
			}
		}
	}

	// Apply excise duty and credit wallet
	amountKES := float64(mpesaReq.AmountKobo) / 100
	excise     := amountKES * 0.05
	creditKES  := amountKES - excise
	creditKobo := int64(creditKES * 100)
	exciseKobo := int64(excise * 100)

	tx, err := s.db.Beginx()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{})
		return
	}
	defer tx.Rollback()

	// Credit wallet
	_, err = s.walletSvc.Credit(
		tx, mpesaReq.UserID, creditKobo,
		"deposit",
		fmt.Sprintf("M-Pesa deposit · %s", receipt),
		nil, &receipt,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{})
		return
	}

	// Record excise as debit record (compliance)
	_, _ = s.walletSvc.Debit(tx, mpesaReq.UserID, exciseKobo, "excise",
		"Excise duty 5%", nil)

	// Update request status
	tx.Exec(`
		UPDATE mpesa_requests
		SET status = 'completed', mpesa_receipt = $1,
		    result_code = '0', result_desc = $2, completed_at = NOW()
		WHERE id = $3
	`, receipt, resultDesc, mpesaReq.ID)

	tx.Commit()

	// Always respond with success to Safaricom
	c.JSON(http.StatusOK, gin.H{"ResultCode": 0, "ResultDesc": "Accepted"})
}

// ── B2C Withdrawal ────────────────────────────────────────────

type b2cRequest struct {
	InitiatorName      string `json:"InitiatorName"`
	SecurityCredential string `json:"SecurityCredential"`
	CommandID          string `json:"CommandID"`
	Amount             int64  `json:"Amount"`
	PartyA             string `json:"PartyA"` // shortcode
	PartyB             string `json:"PartyB"` // customer phone
	Remarks            string `json:"Remarks"`
	QueueTimeOutURL    string `json:"QueueTimeOutURL"`
	ResultURL          string `json:"ResultURL"`
	Occasion           string `json:"Occasion"`
}

// InitiateWithdrawal sends funds to a customer via B2C.
func (s *Service) InitiateWithdrawal(userID uuid.UUID, amountKES float64, phone string) (*models.MpesaRequest, error) {
	// Apply withdrawal tax (Finance Act 2025)
	tax     := amountKES * 0.05
	netKES  := amountKES - tax

	token, err := s.getAccessToken()
	if err != nil {
		return nil, err
	}

	payload := b2cRequest{
		InitiatorName:      s.cfg.MpesaB2CInitiator,
		SecurityCredential: s.cfg.MpesaB2CPassword,
		CommandID:          "BusinessPayment",
		Amount:             int64(netKES),
		PartyA:             s.cfg.MpesaShortcode,
		PartyB:             phone,
		Remarks:            "Tabiri withdrawal",
		QueueTimeOutURL:    s.cfg.MpesaCallbackURL + "/timeout",
		ResultURL:          s.cfg.MpesaCallbackURL + "/b2c/result",
		Occasion:           "Winnings withdrawal",
	}

	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST",
		s.baseURL()+"/mpesa/b2c/v1/paymentrequest",
		bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("b2c request: %w", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	amountKobo := int64(amountKES * 100)
	var mpesaReq models.MpesaRequest
	err = s.db.QueryRowx(`
		INSERT INTO mpesa_requests
		    (user_id, type, conversation_id, amount_kobo, phone, status)
		VALUES ($1, 'b2c', $2, $3, $4, 'pending')
		RETURNING *
	`, userID, result["ConversationID"], amountKobo, phone).
		StructScan(&mpesaReq)

	return &mpesaReq, err
}

// ── HTTP Handlers ──────────────────────────────────────────────

func (s *Service) RegisterRoutes(r *gin.RouterGroup) {
	// Authenticated
	r.POST("/deposit",  s.handleDeposit)
	r.POST("/withdraw", s.handleWithdraw)

	// Safaricom callbacks — no auth (validated by IP + content)
	r.POST("/callback",        s.HandleDepositCallback)
	r.POST("/b2c/result",      s.handleB2CResult)
	r.POST("/b2c/timeout",     s.handleB2CTimeout)
}

func (s *Service) handleDeposit(c *gin.Context) {
	var req models.DepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)

	// Check daily deposit limit
	if err := s.walletSvc.CheckDailyDepositLimit(userID, int64(req.AmountKES*100)); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}

	mpesaReq, err := s.InitiateDeposit(userID, req.AmountKES, req.Phone)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "M-Pesa request failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":             "STK push sent — check your phone",
		"checkout_request_id": mpesaReq.CheckoutRequestID,
		"amount_kes":          req.AmountKES,
	})
}

func (s *Service) handleWithdraw(c *gin.Context) {
	var req models.WithdrawRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	amountKobo := int64(req.AmountKES * 100)

	// Debit wallet first
	tx, _ := s.db.Beginx()
	defer tx.Rollback()

	_, err := s.walletSvc.Debit(tx, userID, amountKobo, "withdrawal",
		fmt.Sprintf("M-Pesa withdrawal to %s", req.Phone), nil)
	if err != nil {
		status := http.StatusInternalServerError
		if err == wallet.ErrInsufficientFunds {
			status = http.StatusPaymentRequired
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	tx.Commit()

	// Initiate B2C
	_, err = s.InitiateWithdrawal(userID, req.AmountKES, req.Phone)
	if err != nil {
		// Refund wallet on B2C failure
		refundTx, _ := s.db.Beginx()
		s.walletSvc.Credit(refundTx, userID, amountKobo, "deposit", "Withdrawal refund (B2C failed)", nil, nil)
		refundTx.Commit()

		c.JSON(http.StatusBadGateway, gin.H{"error": "withdrawal failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message":    "Withdrawal initiated — funds arrive within minutes",
		"amount_kes": req.AmountKES,
	})
}

func (s *Service) handleB2CResult(c *gin.Context) {
	// TODO: parse B2C result, update request status
	c.JSON(http.StatusOK, gin.H{"ResultCode": 0})
}

func (s *Service) handleB2CTimeout(c *gin.Context) {
	// TODO: handle B2C timeout — retry or alert
	c.JSON(http.StatusOK, gin.H{"ResultCode": 0})
}
