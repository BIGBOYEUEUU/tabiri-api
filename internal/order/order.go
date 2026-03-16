package order

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/tabiri/api/config"
	"github.com/tabiri/api/internal/lmsr"
	"github.com/tabiri/api/internal/middleware"
	"github.com/tabiri/api/internal/models"
	"github.com/tabiri/api/internal/wallet"
)

var (
	ErrMarketClosed   = errors.New("market is not open for trading")
	ErrBelowMinimum   = errors.New("amount below minimum trade size")
)

type Service struct {
	db        *sqlx.DB
	walletSvc *wallet.Service
	cfg       *config.Config
}

func NewService(db *sqlx.DB, walletSvc *wallet.Service, cfg *config.Config) *Service {
	return &Service{db: db, walletSvc: walletSvc, cfg: cfg}
}

// Preview calculates trade details without executing.
func (s *Service) Preview(marketID uuid.UUID, side string, amountKES float64) (*models.TradePreviewResponse, error) {
	var m models.Market
	if err := s.db.Get(&m, `SELECT * FROM markets WHERE id = $1`, marketID); err != nil {
		return nil, fmt.Errorf("market not found: %w", err)
	}
	if m.Status != "open" {
		return nil, ErrMarketClosed
	}

	result := lmsr.ComputeTrade(
		m.QYes, m.QNo, m.LiquidityB,
		side,
		amountKES,
		s.cfg.PlatformFeeRate,
		s.cfg.ExciseDutyRate,
	)

	return &models.TradePreviewResponse{
		MarketID:    marketID.String(),
		Side:        side,
		AmountKES:   amountKES,
		Shares:      result.Shares,
		FeeKES:      result.FeeKES,
		ExciseKES:   result.ExciseKES,
		TotalKES:    result.TotalKES,
		PayoutKES:   result.PayoutKES,
		ProfitKES:   result.ProfitKES,
		ROIPct:      result.ROIPCT,
		PriceImpact: result.PriceImpactPct,
		NewYesPrice: result.NewYesPricePct,
	}, nil
}

// Buy executes a buy order atomically:
// 1. Lock market row
// 2. Compute LMSR shares
// 3. Debit user wallet (amount + fee + excise)
// 4. Update market q_yes/q_no and yes_price
// 5. Upsert position
// 6. Record order
// 7. Record price history point
func (s *Service) Buy(userID uuid.UUID, req *models.BuyOrderRequest) (*models.Order, error) {
	if req.AmountKES < s.cfg.MinTradeKES {
		return nil, ErrBelowMinimum
	}

	marketID, err := uuid.Parse(req.MarketID)
	if err != nil {
		return nil, errors.New("invalid market ID")
	}

	tx, err := s.db.Beginx()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock market
	var m models.Market
	if err := tx.Get(&m, `SELECT * FROM markets WHERE id = $1 FOR UPDATE`, marketID); err != nil {
		return nil, fmt.Errorf("lock market: %w", err)
	}
	if m.Status != "open" {
		return nil, ErrMarketClosed
	}

	// 2. Compute LMSR
	result := lmsr.ComputeTrade(
		m.QYes, m.QNo, m.LiquidityB,
		req.Side,
		req.AmountKES,
		s.cfg.PlatformFeeRate,
		s.cfg.ExciseDutyRate,
	)
	if result.Shares <= 0 {
		return nil, errors.New("trade too small to receive any shares")
	}

	totalKobo   := int64(result.TotalKES * 100)
	amountKobo  := int64(result.CostKES * 100)
	feeKobo     := int64(result.FeeKES * 100)
	exciseKobo  := int64(result.ExciseKES * 100)

	// 3. Debit wallet
	desc := fmt.Sprintf("Bought %s on: %s", req.Side, m.Title)
	_, err = s.walletSvc.Debit(tx, userID, totalKobo, "buy", desc, &marketID)
	if err != nil {
		return nil, fmt.Errorf("wallet debit: %w", err)
	}

	// Record fee as separate transaction for GRA compliance
	_, err = s.walletSvc.Debit(tx, userID, 0, "fee", "Platform fee", &marketID)
	// (fees already included in totalKobo — this is just a record)
	_ = err // non-fatal

	// 4. Update market shares and price
	newQYes, newQNo := m.QYes, m.QNo
	if req.Side == "yes" {
		newQYes += result.Shares
	} else {
		newQNo += result.Shares
	}
	newPrice := lmsr.Price(newQYes, newQNo, m.LiquidityB)

	_, err = tx.Exec(`
		UPDATE markets
		SET q_yes = $1, q_no = $2, yes_price = $3,
		    volume_kobo = volume_kobo + $4,
		    trade_count = trade_count + 1,
		    updated_at = NOW()
		WHERE id = $5
	`, newQYes, newQNo, newPrice, amountKobo, marketID)
	if err != nil {
		return nil, fmt.Errorf("update market: %w", err)
	}

	// 5. Upsert position
	avgCostKobo := int64((req.AmountKES / result.Shares) * 100)
	_, err = tx.Exec(`
		INSERT INTO positions (user_id, market_id, side, shares, avg_cost_kobo, total_cost_kobo)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, market_id, side) DO UPDATE SET
		    shares         = positions.shares + EXCLUDED.shares,
		    total_cost_kobo = positions.total_cost_kobo + EXCLUDED.total_cost_kobo,
		    avg_cost_kobo  = (positions.total_cost_kobo + EXCLUDED.total_cost_kobo) /
		                     NULLIF(positions.shares + EXCLUDED.shares, 0),
		    updated_at     = NOW()
	`, userID, marketID, req.Side, result.Shares, avgCostKobo, amountKobo)
	if err != nil {
		return nil, fmt.Errorf("upsert position: %w", err)
	}

	// 6. Record order
	var o models.Order
	err = tx.QueryRowx(`
		INSERT INTO orders
		    (user_id, market_id, side, order_type, shares, cost_kobo, fee_kobo, excise_kobo, price_at_fill, status)
		VALUES ($1, $2, $3, 'market', $4, $5, $6, $7, $8, 'filled')
		RETURNING *
	`, userID, marketID, req.Side, result.Shares, amountKobo, feeKobo, exciseKobo, newPrice).
		StructScan(&o)
	if err != nil {
		return nil, fmt.Errorf("record order: %w", err)
	}

	// 7. Price history
	_, _ = tx.Exec(`
		INSERT INTO price_history (market_id, probability) VALUES ($1, $2)
	`, marketID, newPrice)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &o, nil
}

// GetPositions returns all open positions for a user.
func (s *Service) GetPositions(userID uuid.UUID) ([]models.Position, error) {
	var positions []models.Position
	err := s.db.Select(&positions, `
		SELECT p.* FROM positions p
		JOIN markets m ON m.id = p.market_id
		WHERE p.user_id = $1
		  AND p.settled = FALSE
		  AND m.status != 'cancelled'
		ORDER BY p.created_at DESC
	`, userID)
	return positions, err
}

// ── HTTP Handlers ──────────────────────────────────────────────

func (s *Service) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/preview", s.handlePreview)
	r.POST("/buy",     s.handleBuy)
	r.GET("/positions", s.handleGetPositions)
}

func (s *Service) handlePreview(c *gin.Context) {
	var req models.BuyOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	marketID, _ := uuid.Parse(req.MarketID)
	preview, err := s.Preview(marketID, req.Side, req.AmountKES)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrMarketClosed) {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, preview)
}

func (s *Service) handleBuy(c *gin.Context) {
	var req models.BuyOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	order, err := s.Buy(userID, &req)
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, wallet.ErrInsufficientFunds):
			status = http.StatusPaymentRequired
		case errors.Is(err, ErrMarketClosed):
			status = http.StatusConflict
		case errors.Is(err, ErrBelowMinimum):
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"order": order})
}

func (s *Service) handleGetPositions(c *gin.Context) {
	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	positions, err := s.GetPositions(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get positions"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"positions": positions})
}
