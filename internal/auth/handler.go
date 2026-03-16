package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/tabiri/api/internal/models"
)

type Handler struct {
	db      *sqlx.DB
	authSvc *Service
}

func NewHandler(db *sqlx.DB, authSvc *Service) *Handler {
	return &Handler{db: db, authSvc: authSvc}
}

func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/register", h.handleRegister)
	r.POST("/login",    h.handleLogin)
	r.POST("/refresh",  h.handleRefresh)
	r.POST("/logout",   h.handleLogout)
}

func (h *Handler) handleRegister(c *gin.Context) {
	var req models.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check duplicate phone/email
	var existing int
	h.db.Get(&existing, `
		SELECT COUNT(*) FROM users WHERE phone = $1 OR email = $2
	`, req.Phone, req.Email)
	if existing > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "phone or email already registered"})
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	tx, err := h.db.Beginx()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}
	defer tx.Rollback()

	// Create user
	var user models.User
	err = tx.QueryRowx(`
		INSERT INTO users (name, phone, email, password_hash, kyc_status)
		VALUES ($1, $2, $3, $4, 'pending')
		RETURNING *
	`, req.Name, req.Phone, req.Email, hash).StructScan(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	// Create wallet
	_, err = tx.Exec(`
		INSERT INTO wallets (user_id) VALUES ($1)
	`, user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create wallet"})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "commit failed"})
		return
	}

	// Generate tokens
	accessToken, refreshToken, err := h.authSvc.GenerateTokenPair(user.ID, user.KYCStatus)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	h.storeRefreshToken(user.ID, refreshToken)

	c.JSON(http.StatusCreated, gin.H{
		"user": user,
		"tokens": models.TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresIn:    h.authSvc.expiryHours * 3600,
		},
	})
}

func (h *Handler) handleLogin(c *gin.Context) {
	var req models.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user models.User
	err := h.db.Get(&user, `SELECT * FROM users WHERE phone = $1`, req.Phone)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid phone or password"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "db error"})
		return
	}

	if !CheckPassword(user.PasswordHash, req.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid phone or password"})
		return
	}

	if user.SuspendedAt != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "account suspended"})
		return
	}

	accessToken, refreshToken, err := h.authSvc.GenerateTokenPair(user.ID, user.KYCStatus)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	h.storeRefreshToken(user.ID, refreshToken)

	c.JSON(http.StatusOK, gin.H{
		"user": user,
		"tokens": models.TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresIn:    h.authSvc.expiryHours * 3600,
		},
	})
}

func (h *Handler) handleRefresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	tokenHash := hashToken(req.RefreshToken)
	var row struct {
		UserID    uuid.UUID `db:"user_id"`
		ExpiresAt time.Time `db:"expires_at"`
		Revoked   bool      `db:"revoked"`
	}
	err := h.db.Get(&row, `
		SELECT user_id, expires_at, revoked FROM refresh_tokens
		WHERE token_hash = $1
	`, tokenHash)
	if err != nil || row.Revoked || time.Now().After(row.ExpiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired refresh token"})
		return
	}

	var user models.User
	h.db.Get(&user, `SELECT * FROM users WHERE id = $1`, row.UserID)

	// Revoke old token, issue new pair
	h.db.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`, tokenHash)

	accessToken, refreshToken, _ := h.authSvc.GenerateTokenPair(user.ID, user.KYCStatus)
	h.storeRefreshToken(user.ID, refreshToken)

	c.JSON(http.StatusOK, gin.H{
		"tokens": models.TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresIn:    h.authSvc.expiryHours * 3600,
		},
	})
}

func (h *Handler) handleLogout(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	c.ShouldBindJSON(&req)
	if req.RefreshToken != "" {
		h.db.Exec(`UPDATE refresh_tokens SET revoked = TRUE WHERE token_hash = $1`,
			hashToken(req.RefreshToken))
	}
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func (h *Handler) storeRefreshToken(userID uuid.UUID, token string) {
	h.db.Exec(`
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, hashToken(token), time.Now().Add(h.authSvc.RefreshExpiry()))
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
