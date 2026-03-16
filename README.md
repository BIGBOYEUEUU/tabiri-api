# Tabiri API

Go backend for the Tabiri prediction market platform.

## Stack

- **Go 1.22** — language
- **Gin** — HTTP router
- **PostgreSQL 16** — primary database
- **Redis 7** — rate limiting + caching (coming soon)
- **JWT** — authentication
- **M-Pesa Daraja API** — payments
- **Smile Identity** — KYC verification

## Project structure

```
tabiri-api/
├── cmd/server/          → main entry point
├── config/              → environment config
├── db/
│   └── migrations/      → SQL schema
├── internal/
│   ├── auth/            → JWT service + login/register handlers
│   ├── market/          → market CRUD + price history
│   ├── order/           → LMSR buy engine + positions
│   ├── wallet/          → balance, credit/debit, transactions
│   ├── mpesa/           → Daraja STK Push + B2C
│   ├── settlement/      → market resolution + payouts
│   ├── gra/             → GRA regulatory monitoring
│   ├── lmsr/            → LMSR matching engine (pure math)
│   └── middleware/      → JWT auth, KYC check, CORS
└── docker-compose.yml   → local Postgres + Redis
```

## Quick start

**Prerequisites:** Go 1.22+, Docker Desktop

```bash
# 1. Install dependencies
go mod tidy

# 2. Start Postgres + Redis
docker-compose up -d

# 3. Copy env file
cp .env.example .env
# Edit .env with your M-Pesa and Smile Identity credentials

# 4. Run the server
go run cmd/server/main.go

# → Server running on http://localhost:8080
```

## API endpoints

### Auth
| Method | Path | Description |
|---|---|---|
| POST | `/v1/auth/register` | Create account |
| POST | `/v1/auth/login` | Login |
| POST | `/v1/auth/refresh` | Refresh token |
| POST | `/v1/auth/logout` | Logout |

### Markets
| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/v1/markets` | — | List open markets |
| GET | `/v1/markets/:id` | — | Get market detail |
| GET | `/v1/markets/:id/history` | — | Price history |
| POST | `/v1/admin/markets` | Admin | Create market |

### Trading
| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/v1/orders/preview` | JWT | Preview trade cost |
| POST | `/v1/orders/buy` | JWT + KYC | Execute buy order |
| GET | `/v1/orders/positions` | JWT | Open positions |

### Wallet
| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/v1/wallet` | JWT | Balance |
| GET | `/v1/wallet/transactions` | JWT | Transaction history |
| PUT | `/v1/wallet/limits` | JWT | Set deposit limit |

### M-Pesa
| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/v1/mpesa/deposit` | JWT | Initiate STK Push |
| POST | `/v1/mpesa/withdraw` | JWT | Initiate withdrawal |
| POST | `/v1/mpesa/callback` | — | Safaricom callback |

### Admin
| Method | Path | Description |
|---|---|---|
| POST | `/v1/admin/markets/:id/resolve` | Resolve market |
| POST | `/v1/admin/markets/:id/cancel` | Cancel market |
| GET | `/v1/admin/gra/events` | GRA monitoring feed |
| GET | `/v1/admin/gra/summary` | Monthly compliance report |

## Running tests

```bash
go test ./...
# Or just the LMSR engine:
go test ./internal/lmsr/...
```

## Deploying to production

We recommend **Railway** or **Render** for the Go API:

1. Push to GitHub
2. Connect repo to Railway/Render
3. Add environment variables from `.env.example`
4. Set `ENVIRONMENT=production`
5. Railway auto-detects Go and deploys

For the database, use **Supabase** (free tier) or **Railway Postgres**.
