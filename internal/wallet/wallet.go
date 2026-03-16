package wallet

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/tabiri/api/internal/middleware"
	"github.com/tabiri/api/internal/models"
)

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrDepositLimitExceeded = errors.New("daily deposit limit exceeded")
)

type Service struct {
	db *sqlx.DB
}

func NewService(db *sqlx.DB) *Service {
	return &Service{db: db}
}

// GetWallet retrieves a user's wallet.
func (s *Service) GetWallet(userID uuid.UUID) (*models.Wallet, error) {
	var w models.Wallet
	err := s.db.Get(&w, `SELECT * FROM wallets WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("get wallet: %w", err)
	}
	return &w, nil
}

// Credit adds funds to a wallet atomically and records the transaction.
// amountKobo must be positive.
func (s *Service) Credit(
	tx *sqlx.Tx,
	userID uuid.UUID,
	amountKobo int64,
	txType, description string,
	marketID *uuid.UUID,
	mpesaRef *string,
) (*models.Transaction, error) {
	if amountKobo <= 0 {
		return nil, errors.New("credit amount must be positive")
	}

	var w models.Wallet
	err := tx.Get(&w, `
		SELECT * FROM wallets WHERE user_id = $1 FOR UPDATE
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("lock wallet: %w", err)
	}

	newBalance := w.BalanceKobo + amountKobo

	_, err = tx.Exec(`
		UPDATE wallets
		SET balance_kobo = $1,
		    total_deposited = CASE WHEN $3 = 'deposit' THEN total_deposited + $2 ELSE total_deposited END,
		    updated_at = NOW()
		WHERE user_id = $4
	`, newBalance, amountKobo, txType, userID)
	if err != nil {
		return nil, fmt.Errorf("update wallet balance: %w", err)
	}

	return s.recordTransaction(tx, userID, w.ID, txType, amountKobo, newBalance, description, marketID, mpesaRef)
}

// Debit removes funds from a wallet atomically and records the transaction.
// amountKobo must be positive (will be stored as negative in transactions).
func (s *Service) Debit(
	tx *sqlx.Tx,
	userID uuid.UUID,
	amountKobo int64,
	txType, description string,
	marketID *uuid.UUID,
) (*models.Transaction, error) {
	if amountKobo <= 0 {
		return nil, errors.New("debit amount must be positive")
	}

	var w models.Wallet
	err := tx.Get(&w, `
		SELECT * FROM wallets WHERE user_id = $1 FOR UPDATE
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("lock wallet: %w", err)
	}

	if w.BalanceKobo < amountKobo {
		return nil, ErrInsufficientFunds
	}

	newBalance := w.BalanceKobo - amountKobo

	_, err = tx.Exec(`
		UPDATE wallets
		SET balance_kobo = $1,
		    total_withdrawn = CASE WHEN $3 = 'withdrawal' THEN total_withdrawn + $2 ELSE total_withdrawn END,
		    updated_at = NOW()
		WHERE user_id = $4
	`, newBalance, amountKobo, txType, userID)
	if err != nil {
		return nil, fmt.Errorf("update wallet balance: %w", err)
	}

	return s.recordTransaction(tx, userID, w.ID, txType, -amountKobo, newBalance, description, marketID, nil)
}

func (s *Service) recordTransaction(
	tx *sqlx.Tx,
	userID, walletID uuid.UUID,
	txType string,
	amountKobo, balanceAfterKobo int64,
	description string,
	marketID *uuid.UUID,
	mpesaRef *string,
) (*models.Transaction, error) {
	var t models.Transaction
	err := tx.QueryRowx(`
		INSERT INTO transactions
		    (user_id, wallet_id, type, amount_kobo, balance_after_kobo, description, market_id, mpesa_ref, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'completed')
		RETURNING *
	`, userID, walletID, txType, amountKobo, balanceAfterKobo, description, marketID, mpesaRef).
		StructScan(&t)
	if err != nil {
		return nil, fmt.Errorf("record transaction: %w", err)
	}
	return &t, nil
}

// GetTransactions returns the last N transactions for a user.
func (s *Service) GetTransactions(userID uuid.UUID, limit, offset int) ([]models.Transaction, error) {
	var txns []models.Transaction
	err := s.db.Select(&txns, `
		SELECT * FROM transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	return txns, err
}

// CheckDailyDepositLimit returns an error if the user would exceed their daily limit.
func (s *Service) CheckDailyDepositLimit(userID uuid.UUID, amountKobo int64) error {
	var w models.Wallet
	if err := s.db.Get(&w, `SELECT * FROM wallets WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if w.DepositLimitKobo == nil {
		return nil // no limit set
	}

	// Sum deposits in last 24h
	var sumKobo int64
	err := s.db.Get(&sumKobo, `
		SELECT COALESCE(SUM(amount_kobo), 0)
		FROM transactions
		WHERE user_id = $1
		  AND type = 'deposit'
		  AND status = 'completed'
		  AND created_at >= NOW() - INTERVAL '24 hours'
	`, userID)
	if err != nil {
		return err
	}

	if sumKobo+amountKobo > *w.DepositLimitKobo {
		return ErrDepositLimitExceeded
	}
	return nil
}

// ── HTTP Handlers ──────────────────────────────────────────────

func (s *Service) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("",            s.handleGetWallet)
	r.GET("/transactions", s.handleGetTransactions)
	r.PUT("/limits",     s.handleSetDepositLimit)
}

func (s *Service) handleGetWallet(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	w, err := s.GetWallet(userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get wallet"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"wallet": w,
		"balance_kes": float64(w.BalanceKobo) / 100,
	})
}

func (s *Service) handleGetTransactions(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	txns, err := s.GetTransactions(userID, 50, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get transactions"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"transactions": txns})
}

func (s *Service) handleSetDepositLimit(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	var req struct {
		LimitKES *float64 `json:"limit_kes"` // nil = remove limit
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var limitKobo *int64
	if req.LimitKES != nil {
		v := int64(*req.LimitKES * 100)
		limitKobo = &v
	}

	_, err := s.db.Exec(`
		UPDATE wallets SET deposit_limit_daily_kobo = $1, updated_at = $2
		WHERE user_id = $3
	`, limitKobo, time.Now(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update limit"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deposit limit updated"})
}
