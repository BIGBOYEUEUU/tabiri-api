package models

import (
	"time"

	"github.com/google/uuid"
)

// ── Users ─────────────────────────────────────────────────────

type User struct {
	ID            uuid.UUID  `db:"id"             json:"id"`
	Name          string     `db:"name"           json:"name"`
	Phone         string     `db:"phone"          json:"phone"`
	Email         string     `db:"email"          json:"email"`
	PasswordHash  string     `db:"password_hash"  json:"-"`
	KYCStatus     string     `db:"kyc_status"     json:"kyc_status"`
	DateOfBirth   *time.Time `db:"date_of_birth"  json:"date_of_birth,omitempty"`
	CreatedAt     time.Time  `db:"created_at"     json:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"     json:"updated_at"`
	SuspendedAt     *time.Time `db:"suspended_at"     json:"suspended_at,omitempty"`
	SuspendedReason *string    `db:"suspended_reason" json:"suspended_reason,omitempty"`
}

// ── Wallets ───────────────────────────────────────────────────

type Wallet struct {
	ID                   uuid.UUID `db:"id"                      json:"id"`
	UserID               uuid.UUID `db:"user_id"                 json:"user_id"`
	BalanceKobo          int64     `db:"balance_kobo"            json:"balance_kobo"`
	PendingKobo          int64     `db:"pending_kobo"            json:"pending_kobo"`
	TotalDepositedKobo   int64     `db:"total_deposited"         json:"total_deposited"`
	TotalWithdrawnKobo   int64     `db:"total_withdrawn"         json:"total_withdrawn"`
	DepositLimitKobo     *int64    `db:"deposit_limit_daily_kobo" json:"deposit_limit_daily_kobo,omitempty"`
	CreatedAt            time.Time `db:"created_at"              json:"created_at"`
	UpdatedAt            time.Time `db:"updated_at"              json:"updated_at"`
}

// BalanceKES returns balance as a float in KES.
func (w *Wallet) BalanceKES() float64 { return float64(w.BalanceKobo) / 100 }

// ── Transactions ──────────────────────────────────────────────

type Transaction struct {
	ID               uuid.UUID  `db:"id"                json:"id"`
	UserID           uuid.UUID  `db:"user_id"           json:"user_id"`
	WalletID         uuid.UUID  `db:"wallet_id"         json:"wallet_id"`
	Type             string     `db:"type"              json:"type"`
	AmountKobo       int64      `db:"amount_kobo"       json:"amount_kobo"`
	BalanceAfterKobo int64      `db:"balance_after_kobo" json:"balance_after_kobo"`
	Description      string     `db:"description"       json:"description"`
	MarketID         *uuid.UUID `db:"market_id"         json:"market_id,omitempty"`
	MpesaRef         *string    `db:"mpesa_ref"         json:"mpesa_ref,omitempty"`
	Status           string     `db:"status"            json:"status"`
	CreatedAt        time.Time  `db:"created_at"        json:"created_at"`
}

// ── Markets ───────────────────────────────────────────────────

type Market struct {
	ID                 uuid.UUID  `db:"id"                  json:"id"`
	Title              string     `db:"title"               json:"title"`
	Description        *string    `db:"description"         json:"description,omitempty"`
	ResolutionRules    *string    `db:"resolution_rules"    json:"resolution_rules,omitempty"`
	Category           string     `db:"category"            json:"category"`
	Status             string     `db:"status"              json:"status"`
	LiquidityB         float64    `db:"liquidity_b"         json:"liquidity_b"`
	QYes               float64    `db:"q_yes"               json:"q_yes"`
	QNo                float64    `db:"q_no"                json:"q_no"`
	YesPrice           float64    `db:"yes_price"           json:"yes_price"`
	VolumeKobo         int64      `db:"volume_kobo"         json:"volume_kobo"`
	TradeCount         int        `db:"trade_count"         json:"trade_count"`
	ClosesAt           time.Time  `db:"closes_at"           json:"closes_at"`
	ResolvedAt         *time.Time `db:"resolved_at"         json:"resolved_at,omitempty"`
	Outcome            *string    `db:"outcome"             json:"outcome,omitempty"`
	ResolutionEvidence *string    `db:"resolution_evidence" json:"resolution_evidence,omitempty"`
	CreatedBy          *uuid.UUID `db:"created_by"          json:"created_by,omitempty"`
	CreatedAt          time.Time  `db:"created_at"          json:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at"          json:"updated_at"`
}

// ── Price History ─────────────────────────────────────────────

type PricePoint struct {
	ID          uuid.UUID `db:"id"           json:"id"`
	MarketID    uuid.UUID `db:"market_id"    json:"market_id"`
	Probability float64   `db:"probability"  json:"probability"`
	RecordedAt  time.Time `db:"recorded_at"  json:"recorded_at"`
}

// ── Positions ─────────────────────────────────────────────────

type Position struct {
	ID             uuid.UUID  `db:"id"              json:"id"`
	UserID         uuid.UUID  `db:"user_id"         json:"user_id"`
	MarketID       uuid.UUID  `db:"market_id"       json:"market_id"`
	Side           string     `db:"side"            json:"side"`
	Shares         float64    `db:"shares"          json:"shares"`
	AvgCostKobo    int64      `db:"avg_cost_kobo"   json:"avg_cost_kobo"`
	TotalCostKobo  int64      `db:"total_cost_kobo" json:"total_cost_kobo"`
	Settled        bool       `db:"settled"         json:"settled"`
	PayoutKobo     *int64     `db:"payout_kobo"     json:"payout_kobo,omitempty"`
	CreatedAt      time.Time  `db:"created_at"      json:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"      json:"updated_at"`
}

// ── Orders ────────────────────────────────────────────────────

type Order struct {
	ID           uuid.UUID `db:"id"            json:"id"`
	UserID       uuid.UUID `db:"user_id"       json:"user_id"`
	MarketID     uuid.UUID `db:"market_id"     json:"market_id"`
	Side         string    `db:"side"          json:"side"`
	OrderType    string    `db:"order_type"    json:"order_type"`
	Shares       float64   `db:"shares"        json:"shares"`
	CostKobo     int64     `db:"cost_kobo"     json:"cost_kobo"`
	FeeKobo      int64     `db:"fee_kobo"      json:"fee_kobo"`
	ExciseKobo   int64     `db:"excise_kobo"   json:"excise_kobo"`
	PriceAtFill  float64   `db:"price_at_fill" json:"price_at_fill"`
	Status       string    `db:"status"        json:"status"`
	CreatedAt    time.Time `db:"created_at"    json:"created_at"`
}

// ── M-Pesa ────────────────────────────────────────────────────

type MpesaRequest struct {
	ID                        uuid.UUID  `db:"id"                          json:"id"`
	UserID                    uuid.UUID  `db:"user_id"                     json:"user_id"`
	Type                      string     `db:"type"                        json:"type"`
	MerchantRequestID         *string    `db:"merchant_request_id"         json:"merchant_request_id,omitempty"`
	CheckoutRequestID         *string    `db:"checkout_request_id"         json:"checkout_request_id,omitempty"`
	ConversationID            *string    `db:"conversation_id"             json:"conversation_id,omitempty"`
	OriginatorConversationID  *string    `db:"originator_conversation_id"  json:"originator_conversation_id,omitempty"`
	AmountKobo                int64      `db:"amount_kobo"                 json:"amount_kobo"`
	Phone                     string     `db:"phone"                       json:"phone"`
	Status                    string     `db:"status"                      json:"status"`
	MpesaReceipt              *string    `db:"mpesa_receipt"               json:"mpesa_receipt,omitempty"`
	ResultCode                *string    `db:"result_code"                 json:"result_code,omitempty"`
	ResultDesc                *string    `db:"result_desc"                 json:"result_desc,omitempty"`
	CreatedAt                 time.Time  `db:"created_at"                  json:"created_at"`
	CompletedAt               *time.Time `db:"completed_at"                json:"completed_at,omitempty"`
}

// ── Auth ──────────────────────────────────────────────────────

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
}

// ── API Request/Response shapes ───────────────────────────────

type RegisterRequest struct {
	Name        string `json:"name"          binding:"required,min=2"`
	Phone       string `json:"phone"         binding:"required"`
	Email       string `json:"email"         binding:"required,email"`
	Password    string `json:"password"      binding:"required,min=8"`
	DateOfBirth string `json:"date_of_birth" binding:"required"` // YYYY-MM-DD
}

type LoginRequest struct {
	Phone    string `json:"phone"    binding:"required"`
	Password string `json:"password" binding:"required"`
}

type BuyOrderRequest struct {
	MarketID string  `json:"market_id" binding:"required,uuid"`
	Side     string  `json:"side"      binding:"required,oneof=yes no"`
	AmountKES float64 `json:"amount_kes" binding:"required,min=50"`
}

type SellOrderRequest struct {
	MarketID string  `json:"market_id" binding:"required,uuid"`
	Side     string  `json:"side"      binding:"required,oneof=yes no"`
	Shares   float64 `json:"shares"    binding:"required,gt=0"`
}

type DepositRequest struct {
	AmountKES float64 `json:"amount_kes" binding:"required,min=50"`
	Phone     string  `json:"phone"      binding:"required"`
}

type WithdrawRequest struct {
	AmountKES float64 `json:"amount_kes" binding:"required,min=100"`
	Phone     string  `json:"phone"      binding:"required"`
}

type ResolveMarketRequest struct {
	Outcome  string `json:"outcome"   binding:"required,oneof=yes no"`
	Evidence string `json:"evidence"  binding:"required"`
}

type CreateMarketRequest struct {
	Title           string  `json:"title"            binding:"required,min=10"`
	Description     string  `json:"description"      binding:"required"`
	ResolutionRules string  `json:"resolution_rules" binding:"required"`
	Category        string  `json:"category"         binding:"required,oneof=politics economics football entertainment weather other"`
	ClosesAt        string  `json:"closes_at"        binding:"required"` // RFC3339
	LiquidityB      float64 `json:"liquidity_b"`                          // defaults to 100
}

type TradePreviewResponse struct {
	MarketID    string  `json:"market_id"`
	Side        string  `json:"side"`
	AmountKES   float64 `json:"amount_kes"`
	Shares      float64 `json:"shares"`
	FeeKES      float64 `json:"fee_kes"`
	ExciseKES   float64 `json:"excise_kes"`
	TotalKES    float64 `json:"total_kes"`
	PayoutKES   float64 `json:"payout_kes"`   // if win
	ProfitKES   float64 `json:"profit_kes"`   // if win
	ROIPct      float64 `json:"roi_pct"`      // if win
	PriceImpact float64 `json:"price_impact"` // % price movement
	NewYesPrice float64 `json:"new_yes_price"`
}
