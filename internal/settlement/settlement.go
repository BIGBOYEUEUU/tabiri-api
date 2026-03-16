package settlement

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/tabiri/api/internal/middleware"
	"github.com/tabiri/api/internal/models"
	"github.com/tabiri/api/internal/wallet"
)

type Service struct {
	db        *sqlx.DB
	walletSvc *wallet.Service
}

func NewService(db *sqlx.DB, walletSvc *wallet.Service) *Service {
	return &Service{db: db, walletSvc: walletSvc}
}

// Resolve resolves a market and distributes payouts to winning positions.
// Each winning share pays out KES 100.
// This runs in a single database transaction.
func (s *Service) Resolve(marketID uuid.UUID, outcome, evidence string, resolvedBy uuid.UUID) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Lock and validate market
	var m models.Market
	if err := tx.Get(&m, `SELECT * FROM markets WHERE id = $1 FOR UPDATE`, marketID); err != nil {
		return fmt.Errorf("market not found: %w", err)
	}
	if m.Status == "resolved" {
		return errors.New("market already resolved")
	}
	if m.Status != "open" && m.Status != "closed" {
		return fmt.Errorf("market cannot be resolved in status: %s", m.Status)
	}

	// 2. Mark market as resolving
	_, err = tx.Exec(`
		UPDATE markets
		SET status = 'resolving', outcome = $1,
		    resolution_evidence = $2,
		    resolved_at = NOW(), updated_at = NOW()
		WHERE id = $3
	`, outcome, evidence, marketID)
	if err != nil {
		return fmt.Errorf("update market status: %w", err)
	}

	// 3. Load all winning positions
	var positions []models.Position
	err = tx.Select(&positions, `
		SELECT * FROM positions
		WHERE market_id = $1 AND side = $2 AND settled = FALSE
		FOR UPDATE
	`, marketID, outcome)
	if err != nil {
		return fmt.Errorf("load positions: %w", err)
	}

	log.Printf("Resolving market %s as %s — %d winning positions", marketID, outcome, len(positions))

	// 4. Pay out each winning position (KES 100 per share)
	totalPayoutKobo := int64(0)
	for _, pos := range positions {
		payoutKobo := int64(pos.Shares * 100 * 100) // shares × KES 100 × 100 kobo/KES

		desc := fmt.Sprintf("Payout — %s resolved %s (%.0f shares × KES 100)", m.Title, outcome, pos.Shares)
		_, err := s.walletSvc.Credit(tx, pos.UserID, payoutKobo, "payout", desc, &marketID, nil)
		if err != nil {
			log.Printf("ERROR paying user %s: %v", pos.UserID, err)
			continue // don't fail the whole settlement
		}

		// Mark position as settled
		_, err = tx.Exec(`
			UPDATE positions
			SET settled = TRUE, payout_kobo = $1, updated_at = NOW()
			WHERE id = $2
		`, payoutKobo, pos.ID)
		if err != nil {
			log.Printf("ERROR marking position settled %s: %v", pos.ID, err)
		}

		totalPayoutKobo += payoutKobo
	}

	// 5. Mark losing positions as settled with zero payout
	_, err = tx.Exec(`
		UPDATE positions
		SET settled = TRUE, payout_kobo = 0, updated_at = NOW()
		WHERE market_id = $1 AND settled = FALSE
	`, marketID)
	if err != nil {
		return fmt.Errorf("settle losing positions: %w", err)
	}

	// 6. Mark market as resolved
	_, err = tx.Exec(`
		UPDATE markets SET status = 'resolved', updated_at = NOW() WHERE id = $1
	`, marketID)
	if err != nil {
		return fmt.Errorf("finalize market: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	log.Printf("✓ Market %s resolved. Total payout: KES %.2f to %d positions",
		marketID, float64(totalPayoutKobo)/100, len(positions))
	return nil
}

// CancelMarket cancels a market and refunds all positions.
func (s *Service) CancelMarket(marketID uuid.UUID) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var m models.Market
	if err := tx.Get(&m, `SELECT * FROM markets WHERE id = $1 FOR UPDATE`, marketID); err != nil {
		return fmt.Errorf("market not found: %w", err)
	}
	if m.Status == "resolved" || m.Status == "cancelled" {
		return fmt.Errorf("cannot cancel market in status: %s", m.Status)
	}

	// Refund all unsettled positions
	var positions []models.Position
	tx.Select(&positions, `
		SELECT * FROM positions WHERE market_id = $1 AND settled = FALSE FOR UPDATE
	`, marketID)

	for _, pos := range positions {
		refundKobo := pos.TotalCostKobo
		desc := fmt.Sprintf("Refund — %s (cancelled)", m.Title)
		s.walletSvc.Credit(tx, pos.UserID, refundKobo, "payout", desc, &marketID, nil)
		tx.Exec(`UPDATE positions SET settled = TRUE, payout_kobo = $1 WHERE id = $2`,
			refundKobo, pos.ID)
	}

	tx.Exec(`UPDATE markets SET status = 'cancelled', updated_at = NOW() WHERE id = $1`, marketID)
	return tx.Commit()
}

// CheckExpiredMarkets closes any markets past their closing time.
// Run this on a scheduler (e.g., every 5 minutes).
func (s *Service) CheckExpiredMarkets() error {
	result, err := s.db.Exec(`
		UPDATE markets
		SET status = 'closed', updated_at = NOW()
		WHERE status = 'open' AND closes_at < NOW()
	`)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n > 0 {
		log.Printf("Closed %d expired markets", n)
	}
	return nil
}

// StartScheduler runs periodic market maintenance tasks.
func (s *Service) StartScheduler() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			if err := s.CheckExpiredMarkets(); err != nil {
				log.Printf("scheduler error: %v", err)
			}
		}
	}()
	log.Println("✓ Settlement scheduler started")
}

// ── HTTP Handlers ──────────────────────────────────────────────

func (s *Service) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.POST("/:id/resolve", s.handleResolve)
	r.POST("/:id/cancel",  s.handleCancel)
}

func (s *Service) handleResolve(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid market ID"})
		return
	}

	var req models.ResolveMarketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	adminID := c.MustGet(middleware.UserIDKey).(uuid.UUID)
	if err := s.Resolve(id, req.Outcome, req.Evidence, adminID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "market resolved", "outcome": req.Outcome})
}

func (s *Service) handleCancel(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid market ID"})
		return
	}
	if err := s.CancelMarket(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "market cancelled and positions refunded"})
}
