package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tabiri/api/internal/auth"
)

const UserIDKey = "user_id"
const KYCStatusKey = "kyc_status"

// Authenticate validates the JWT Bearer token on every protected route.
func Authenticate(authSvc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		tokenStr := strings.TrimPrefix(header, "Bearer ")
		claims, err := authSvc.ValidateAccessToken(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		c.Set(UserIDKey, claims.UserID)
		c.Set(KYCStatusKey, claims.KYCStatus)
		c.Next()
	}
}

// RequireKYC blocks access if the user is not KYC verified.
// Must be used after Authenticate.
func RequireKYC() gin.HandlerFunc {
	return func(c *gin.Context) {
		status, _ := c.Get(KYCStatusKey)
		if status != "verified" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "identity verification required",
				"code":  "kyc_required",
			})
			return
		}
		c.Next()
	}
}

// CORS sets permissive CORS headers for the frontend.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// RateLimit is a placeholder — replace with Redis-backed limiter in production.
func RateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		// TODO: implement Redis sliding window rate limiter
		// For now, pass through
		c.Next()
	}
}
