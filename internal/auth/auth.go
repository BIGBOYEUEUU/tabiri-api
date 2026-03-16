package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidToken = errors.New("invalid or expired token")
	ErrUnauthorized = errors.New("unauthorized")
)

type Claims struct {
	UserID    uuid.UUID `json:"user_id"`
	KYCStatus string    `json:"kyc_status"`
	jwt.RegisteredClaims
}

type Service struct {
	secret          []byte
	expiryHours     int
	refreshExpHours int
}

func NewService(secret string, expiryHours, refreshExpHours int) *Service {
	return &Service{
		secret:          []byte(secret),
		expiryHours:     expiryHours,
		refreshExpHours: refreshExpHours,
	}
}

// HashPassword returns a bcrypt hash of the password.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword verifies a password against a hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateTokenPair creates access + refresh token pair for a user.
func (s *Service) GenerateTokenPair(userID uuid.UUID, kycStatus string) (accessToken, refreshToken string, err error) {
	// Access token
	claims := &Claims{
		UserID:    userID,
		KYCStatus: kycStatus,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(s.expiryHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "tabiri",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	accessToken, err = token.SignedString(s.secret)
	if err != nil {
		return "", "", err
	}

	// Refresh token — random 32-byte hex string
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	refreshToken = hex.EncodeToString(b)

	return accessToken, refreshToken, nil
}

// ValidateAccessToken parses and validates a JWT, returning claims.
func (s *Service) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// RefreshExpiry returns the refresh token expiry duration.
func (s *Service) RefreshExpiry() time.Duration {
	return time.Duration(s.refreshExpHours) * time.Hour
}
