package config

import (
	"os"
	"strconv"
)

type Config struct {
	// Server
	Port        string
	Environment string

	// Database
	DatabaseURL string

	// Redis
	RedisURL string

	// JWT
	JWTSecret          string
	JWTExpiryHours     int
	RefreshExpiryHours int

	// M-Pesa Daraja
	MpesaConsumerKey    string
	MpesaConsumerSecret string
	MpesaShortcode      string
	MpesaPasskey        string
	MpesaCallbackURL    string
	MpesaB2CInitiator   string
	MpesaB2CPassword    string
	MpesaEnv            string // "sandbox" or "production"

	// Smile Identity (KYC)
	SmilePartnerID string
	SmileAPIKey    string

	// Platform
	PlatformFeeRate   float64 // 0.035 = 3.5%
	MinTradeKES       float64 // 50
	ExciseDutyRate    float64 // 0.05 = 5%
	WithdrawalTaxRate float64 // 0.05 = 5%
}

func Load() *Config {
	return &Config{
		Port:        getEnv("PORT", "8080"),
		Environment: getEnv("ENVIRONMENT", "development"),

		DatabaseURL: getEnv("DATABASE_URL", "postgres://tabiri:tabiri@localhost:5432/tabiri?sslmode=disable"),
		RedisURL:    getEnv("REDIS_URL", "redis://localhost:6379"),

		JWTSecret:          getEnv("JWT_SECRET", "change-me-in-production"),
		JWTExpiryHours:     getEnvInt("JWT_EXPIRY_HOURS", 720), // 30 days
		RefreshExpiryHours: getEnvInt("REFRESH_EXPIRY_HOURS", 2160),

		MpesaConsumerKey:    getEnv("MPESA_CONSUMER_KEY", ""),
		MpesaConsumerSecret: getEnv("MPESA_CONSUMER_SECRET", ""),
		MpesaShortcode:      getEnv("MPESA_SHORTCODE", "174379"),
		MpesaPasskey:        getEnv("MPESA_PASSKEY", ""),
		MpesaCallbackURL:    getEnv("MPESA_CALLBACK_URL", "https://api.tabiri.africa/v1/mpesa/callback"),
		MpesaB2CInitiator:   getEnv("MPESA_B2C_INITIATOR", ""),
		MpesaB2CPassword:    getEnv("MPESA_B2C_PASSWORD", ""),
		MpesaEnv:            getEnv("MPESA_ENV", "sandbox"),

		SmilePartnerID: getEnv("SMILE_PARTNER_ID", ""),
		SmileAPIKey:    getEnv("SMILE_API_KEY", ""),

		PlatformFeeRate:   getEnvFloat("PLATFORM_FEE_RATE", 0.035),
		MinTradeKES:       getEnvFloat("MIN_TRADE_KES", 50),
		ExciseDutyRate:    getEnvFloat("EXCISE_DUTY_RATE", 0.05),
		WithdrawalTaxRate: getEnvFloat("WITHDRAWAL_TAX_RATE", 0.05),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
