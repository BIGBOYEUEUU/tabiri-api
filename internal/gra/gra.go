package gra

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Service handles GRA Gambling Control Act 2025 compliance reporting.
// The GRA requires real-time monitoring of:
// - All deposits and withdrawals above KES 75,000
// - All trades above KES 10,000
// - Player win/loss summaries (monthly)
// - Self-exclusion registrations
type Service struct {
	db *sqlx.DB
}

func NewService(db *sqlx.DB) *Service {
	return &Service{db: db}
}

type EventType string

const (
	EventDeposit        EventType = "deposit"
	EventWithdrawal     EventType = "withdrawal"
	EventTrade          EventType = "trade"
	EventSelfExclusion  EventType = "self_exclusion"
	EventKYCVerified    EventType = "kyc_verified"
	EventSuspicion      EventType = "suspicious_activity"
)

// LogEvent records a GRA-reportable event.
func (s *Service) LogEvent(
	eventType EventType,
	userID *uuid.UUID,
	marketID *uuid.UUID,
	amountKobo int64,
	payload interface{},
) {
	payloadJSON, _ := json.Marshal(payload)

	_, err := s.db.Exec(`
		INSERT INTO gra_events (event_type, user_id, market_id, amount_kobo, payload)
		VALUES ($1, $2, $3, $4, $5)
	`, eventType, userID, marketID, amountKobo, payloadJSON)
	if err != nil {
		log.Printf("GRA log error: %v", err)
	}
}

// GetRecentEvents returns recent GRA events for the regulatory dashboard.
func (s *Service) GetRecentEvents(limit int, since time.Time) ([]map[string]interface{}, error) {
	rows, err := s.db.Queryx(`
		SELECT e.*, u.name as user_name, u.phone as user_phone
		FROM gra_events e
		LEFT JOIN users u ON u.id = e.user_id
		WHERE e.created_at >= $1
		ORDER BY e.created_at DESC
		LIMIT $2
	`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []map[string]interface{}
	for rows.Next() {
		row := make(map[string]interface{})
		if err := rows.MapScan(row); err != nil {
			continue
		}
		events = append(events, row)
	}
	return events, nil
}

// MonthlySummary generates a monthly compliance summary for GRA submission.
func (s *Service) MonthlySummary(year, month int) (map[string]interface{}, error) {
	startDate := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	endDate   := startDate.AddDate(0, 1, 0)

	var summary struct {
		TotalDepositsKobo   int64 `db:"total_deposits"`
		TotalWithdrawalsKobo int64 `db:"total_withdrawals"`
		TotalTradesKobo     int64 `db:"total_trades"`
		UniqueTraders       int   `db:"unique_traders"`
		NewRegistrations    int   `db:"new_registrations"`
		SelfExclusions      int   `db:"self_exclusions"`
	}

	s.db.Get(&summary.TotalDepositsKobo, `
		SELECT COALESCE(SUM(ABS(amount_kobo)),0) FROM transactions
		WHERE type='deposit' AND status='completed'
		AND created_at BETWEEN $1 AND $2
	`, startDate, endDate)

	s.db.Get(&summary.TotalWithdrawalsKobo, `
		SELECT COALESCE(SUM(ABS(amount_kobo)),0) FROM transactions
		WHERE type='withdrawal' AND status='completed'
		AND created_at BETWEEN $1 AND $2
	`, startDate, endDate)

	s.db.Get(&summary.UniqueTraders, `
		SELECT COUNT(DISTINCT user_id) FROM orders
		WHERE created_at BETWEEN $1 AND $2
	`, startDate, endDate)

	s.db.Get(&summary.NewRegistrations, `
		SELECT COUNT(*) FROM users WHERE created_at BETWEEN $1 AND $2
	`, startDate, endDate)

	s.db.Get(&summary.SelfExclusions, `
		SELECT COUNT(*) FROM self_exclusions WHERE created_at BETWEEN $1 AND $2
	`, startDate, endDate)

	return map[string]interface{}{
		"period":              startDate.Format("2006-01"),
		"total_deposits_kes":  float64(summary.TotalDepositsKobo) / 100,
		"total_withdrawals_kes": float64(summary.TotalWithdrawalsKobo) / 100,
		"unique_traders":      summary.UniqueTraders,
		"new_registrations":   summary.NewRegistrations,
		"self_exclusions":     summary.SelfExclusions,
	}, nil
}

// ── HTTP Handlers ──────────────────────────────────────────────

func (s *Service) RegisterAdminRoutes(r *gin.RouterGroup) {
	r.GET("/events",  s.handleGetEvents)
	r.GET("/summary", s.handleMonthlySummary)
}

func (s *Service) handleGetEvents(c *gin.Context) {
	since := time.Now().Add(-24 * time.Hour)
	events, err := s.GetRecentEvents(100, since)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get events"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "count": len(events)})
}

func (s *Service) handleMonthlySummary(c *gin.Context) {
	now := time.Now()
	summary, err := s.MonthlySummary(now.Year(), int(now.Month()))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate summary"})
		return
	}
	c.JSON(http.StatusOK, summary)
}
