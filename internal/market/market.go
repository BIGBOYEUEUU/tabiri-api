package market

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/tabiri/api/internal/lmsr"
	"github.com/tabiri/api/internal/middleware"
	"github.com/tabiri/api/internal/models"
)

type Service struct {
	db *sqlx.DB
}

func NewService(db *sqlx.DB) *Service {
	return &Service{db: db}
}

// GetByID retrieves a single market by ID.
func (s *Service) GetByID(id uuid.UUID) (*models.Market, error) {
	var m models.Market
	err := s.db.Get(&m, `SELECT * FROM markets WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// List returns open markets with optional category filter.
func (s *Service) List(category string, limit, offset int) ([]models.Market, error) {
	var markets []models.Market
	query := `SELECT * FROM markets WHERE status = 'open'`
	args := []interface{}{}

	if category != "" && category != "all" {
		query += ` AND category = $1 ORDER BY volume_kobo DESC LIMIT $2 OFFSET $3`
		args = append(args, category, limit, offset)
	} else {
		query += ` ORDER BY volume_kobo DESC LIMIT $1 OFFSET $2`
		args = append(args, limit, offset)
	}

	err := s.db.Select(&markets, query, args...)
	return markets, err
}

// PriceHistory returns the probability history for a market.
func (s *Service) PriceHistory(marketID uuid.UUID, limit int) ([]models.PricePoint, error) {
	var pts []models.PricePoint
	err := s.db.Select(&pts, `
		SELECT * FROM price_history
		WHERE market_id = $1
		ORDER BY recorded_at ASC
		LIMIT $2
	`, marketID, limit)
	return pts, err
}

// RecordPricePoint stores current probability for charting.
func (s *Service) RecordPricePoint(tx *sqlx.Tx, marketID uuid.UUID, probability float64) error {
	_, err := tx.Exec(`
		INSERT INTO price_history (market_id, probability)
		VALUES ($1, $2)
	`, marketID, probability)
	return err
}

// Create creates a new market (admin only).
func (s *Service) Create(req *models.CreateMarketRequest, createdBy uuid.UUID) (*models.Market, error) {
	closesAt, err := time.Parse(time.RFC3339, req.ClosesAt)
	if err != nil {
		return nil, errors.New("invalid closes_at format, use RFC3339")
	}

	b := req.LiquidityB
	if b <= 0 {
		b = 100
	}

	var m models.Market
	err = s.db.QueryRowx(`
		INSERT INTO markets
		    (title, description, resolution_rules, category, status, liquidity_b, q_yes, q_no, yes_price, closes_at, created_by)
		VALUES ($1, $2, $3, $4, 'open', $5, 0, 0, 50, $6, $7)
		RETURNING *
	`, req.Title, req.Description, req.ResolutionRules, req.Category, b, closesAt, createdBy).
		StructScan(&m)
	if err != nil {
		return nil, err
	}

	// Seed initial price point
	_, _ = s.db.Exec(`
		INSERT INTO price_history (market_id, probability) VALUES ($1, 50)
	`, m.ID)

	return &m, nil
}

// UpdatePriceFromLMSR recalculates and stores the current YES price.
func (s *Service) UpdatePriceFromLMSR(tx *sqlx.Tx, m *models.Market) error {
	newPrice := lmsr.Price(m.QYes, m.QNo, m.LiquidityB)
	_, err := tx.Exec(`
		UPDATE markets
		SET yes_price = $1, updated_at = NOW()
		WHERE id = $2
	`, newPrice, m.ID)
	return err
}

// ── HTTP Handlers ──────────────────────────────────────────────

func (s *Service) RegisterPublicRoutes(r *gin.RouterGroup) {
	r.GET("",        s.handleList)
	r.GET("/:id",    s.handleGet)
	r.GET("/:id/history", s.handlePriceHistory)
}

func (s *Service) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("", s.handleCreate)
}

func (s *Service) handleList(c *gin.Context) {
	category := c.Query("category")
	markets, err := s.List(category, 50, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list markets"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"markets": markets, "count": len(markets)})
}

func (s *Service) handleGet(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid market ID"})
		return
	}

	m, err := s.GetByID(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "market not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get market"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"market": m})
}

func (s *Service) handlePriceHistory(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid market ID"})
		return
	}

	pts, err := s.PriceHistory(id, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get price history"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": pts})
}

func (s *Service) handleCreate(c *gin.Context) {
	var req models.CreateMarketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	m, err := s.Create(&req, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"market": m})
}
