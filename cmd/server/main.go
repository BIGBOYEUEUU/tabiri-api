package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tabiri/api/config"
	"github.com/tabiri/api/db"
	authpkg "github.com/tabiri/api/internal/auth"
	"github.com/tabiri/api/internal/gra"
	"github.com/tabiri/api/internal/market"
	"github.com/tabiri/api/internal/middleware"
	"github.com/tabiri/api/internal/mpesa"
	"github.com/tabiri/api/internal/order"
	"github.com/tabiri/api/internal/settlement"
	"github.com/tabiri/api/internal/wallet"
)

func main() {
	// ── Config ──────────────────────────────────────────────────
	cfg := config.Load()

	// ── Database ─────────────────────────────────────────────────
	database, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer database.Close()

	// ── Services ─────────────────────────────────────────────────
	authSvc       := authpkg.NewService(cfg.JWTSecret, cfg.JWTExpiryHours, cfg.RefreshExpiryHours)
	authHandler   := authpkg.NewHandler(database, authSvc)
	walletSvc     := wallet.NewService(database)
	marketSvc     := market.NewService(database)
	orderSvc      := order.NewService(database, walletSvc, cfg)
	mpesaSvc      := mpesa.NewService(database, walletSvc, cfg)
	settlementSvc := settlement.NewService(database, walletSvc)
	graSvc        := gra.NewService(database)

	// ── Start scheduler ──────────────────────────────────────────
	settlementSvc.StartScheduler()

	// ── Router ───────────────────────────────────────────────────
	if cfg.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.Default()
	r.Use(middleware.CORS())
	r.Use(middleware.RateLimit())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "tabiri-api",
		})
	})

	// ── API v1 ───────────────────────────────────────────────────
	v1 := r.Group("/v1")

	// Auth — public
	authHandler.RegisterRoutes(v1.Group("/auth"))

	// Markets — public read
	marketSvc.RegisterPublicRoutes(v1.Group("/markets"))

	// ── Authenticated routes ─────────────────────────────────────
	authed := v1.Group("")
	authed.Use(middleware.Authenticate(authSvc))

	// Wallet
	walletSvc.RegisterRoutes(authed.Group("/wallet"))

	// M-Pesa — authenticated deposit/withdraw + public callbacks
	authed.POST("/mpesa/deposit",  mpesaSvc.HandleDeposit)
	authed.POST("/mpesa/withdraw", mpesaSvc.HandleWithdraw)
	// Safaricom callbacks — no auth required
	v1.POST("/mpesa/callback",     mpesaSvc.HandleDepositCallback)
	v1.POST("/mpesa/b2c/result",   mpesaSvc.HandleB2CResult)
	v1.POST("/mpesa/b2c/timeout",  mpesaSvc.HandleB2CTimeout)

	// Orders + positions (requires auth + KYC)
	kyc := authed.Group("")
	kyc.Use(middleware.RequireKYC())
	orderSvc.RegisterRoutes(kyc.Group("/orders"))

	// ── Admin routes ──────────────────────────────────────────────
	// TODO: add admin role check middleware
	admin := v1.Group("/admin")
	admin.Use(middleware.Authenticate(authSvc))

	marketSvc.RegisterAdminRoutes(admin.Group("/markets"))
	settlementSvc.RegisterAdminRoutes(admin.Group("/markets"))
	graSvc.RegisterAdminRoutes(admin.Group("/gra"))

	// ── Start server ─────────────────────────────────────────────
	addr := ":" + cfg.Port
	log.Printf("✓ Tabiri API running on %s (env: %s)", addr, cfg.Environment)
	log.Fatal(r.Run(addr))
}
