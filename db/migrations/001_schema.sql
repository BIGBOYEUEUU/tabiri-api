-- ============================================================
-- Tabiri Platform — PostgreSQL Schema
-- Run with: psql $DATABASE_URL -f migrations/001_schema.sql
-- ============================================================

-- Extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ── Users ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name          TEXT NOT NULL,
    phone         TEXT UNIQUE NOT NULL,
    email         TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    kyc_status    TEXT NOT NULL DEFAULT 'pending'
                  CHECK (kyc_status IN ('pending', 'verified', 'rejected')),
    date_of_birth DATE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    suspended_at  TIMESTAMPTZ,
    suspended_reason TEXT
);

CREATE INDEX idx_users_phone ON users(phone);
CREATE INDEX idx_users_email ON users(email);

-- ── Wallets ──────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS wallets (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    balance_kobo      BIGINT NOT NULL DEFAULT 0,  -- stored in kobo (1 KES = 100 kobo)
    pending_kobo      BIGINT NOT NULL DEFAULT 0,  -- pending withdrawals
    total_deposited   BIGINT NOT NULL DEFAULT 0,
    total_withdrawn   BIGINT NOT NULL DEFAULT 0,
    deposit_limit_daily_kobo BIGINT,              -- NULL = no limit
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id)
);

-- ── Transactions ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS transactions (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id       UUID NOT NULL REFERENCES users(id),
    wallet_id     UUID NOT NULL REFERENCES wallets(id),
    type          TEXT NOT NULL CHECK (type IN ('deposit','withdrawal','buy','sell','payout','fee','excise')),
    amount_kobo   BIGINT NOT NULL,       -- positive = credit, negative = debit
    balance_after_kobo BIGINT NOT NULL,  -- running balance snapshot
    description   TEXT NOT NULL,
    market_id     UUID,                  -- nullable, links to market if trade
    mpesa_ref     TEXT,                  -- Safaricom transaction ID
    status        TEXT NOT NULL DEFAULT 'completed'
                  CHECK (status IN ('pending','completed','failed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_transactions_user_id  ON transactions(user_id);
CREATE INDEX idx_transactions_market_id ON transactions(market_id);

-- ── Markets ──────────────────────────────────────────────────
CREATE TYPE market_category AS ENUM (
    'politics', 'economics', 'football', 'entertainment', 'weather', 'other'
);

CREATE TYPE market_status AS ENUM (
    'draft', 'open', 'closed', 'resolving', 'resolved', 'cancelled'
);

CREATE TABLE IF NOT EXISTS markets (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title            TEXT NOT NULL,
    description      TEXT,
    resolution_rules TEXT,
    category         market_category NOT NULL DEFAULT 'other',
    status           market_status NOT NULL DEFAULT 'draft',
    -- LMSR parameters
    liquidity_b      NUMERIC(18,6) NOT NULL DEFAULT 100,  -- LMSR b parameter
    q_yes            NUMERIC(18,6) NOT NULL DEFAULT 0,    -- total YES shares outstanding
    q_no             NUMERIC(18,6) NOT NULL DEFAULT 0,    -- total NO shares outstanding
    -- Pricing
    yes_price        NUMERIC(5,2) NOT NULL DEFAULT 50,    -- current YES probability (0-100)
    -- Volume
    volume_kobo      BIGINT NOT NULL DEFAULT 0,
    trade_count      INTEGER NOT NULL DEFAULT 0,
    -- Timing
    closes_at        TIMESTAMPTZ NOT NULL,
    resolved_at      TIMESTAMPTZ,
    outcome          TEXT CHECK (outcome IN ('yes','no')),
    resolution_evidence TEXT,
    -- Metadata
    created_by       UUID REFERENCES users(id),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_markets_status     ON markets(status);
CREATE INDEX idx_markets_category   ON markets(category);
CREATE INDEX idx_markets_closes_at  ON markets(closes_at);

-- ── Price History ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS price_history (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    market_id   UUID NOT NULL REFERENCES markets(id),
    probability NUMERIC(5,2) NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_price_history_market ON price_history(market_id, recorded_at DESC);

-- ── Positions ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS positions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         UUID NOT NULL REFERENCES users(id),
    market_id       UUID NOT NULL REFERENCES markets(id),
    side            TEXT NOT NULL CHECK (side IN ('yes','no')),
    shares          NUMERIC(18,6) NOT NULL DEFAULT 0,
    avg_cost_kobo   BIGINT NOT NULL DEFAULT 0,   -- per share cost in kobo
    total_cost_kobo BIGINT NOT NULL DEFAULT 0,
    settled         BOOLEAN NOT NULL DEFAULT FALSE,
    payout_kobo     BIGINT,                       -- set on settlement
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, market_id, side)
);

CREATE INDEX idx_positions_user   ON positions(user_id);
CREATE INDEX idx_positions_market ON positions(market_id);

-- ── Orders (trade log) ───────────────────────────────────────
CREATE TABLE IF NOT EXISTS orders (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id       UUID NOT NULL REFERENCES users(id),
    market_id     UUID NOT NULL REFERENCES markets(id),
    side          TEXT NOT NULL CHECK (side IN ('yes','no')),
    order_type    TEXT NOT NULL DEFAULT 'market' CHECK (order_type IN ('market','sell')),
    shares        NUMERIC(18,6) NOT NULL,
    cost_kobo     BIGINT NOT NULL,       -- amount paid/received
    fee_kobo      BIGINT NOT NULL,
    excise_kobo   BIGINT NOT NULL,
    price_at_fill NUMERIC(5,2) NOT NULL, -- probability at time of fill
    status        TEXT NOT NULL DEFAULT 'filled'
                  CHECK (status IN ('pending','filled','cancelled','failed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_orders_user   ON orders(user_id);
CREATE INDEX idx_orders_market ON orders(market_id);

-- ── M-Pesa Requests ──────────────────────────────────────────
CREATE TABLE IF NOT EXISTS mpesa_requests (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id          UUID NOT NULL REFERENCES users(id),
    type             TEXT NOT NULL CHECK (type IN ('stk_push','b2c')),
    merchant_request_id TEXT,
    checkout_request_id TEXT,
    conversation_id  TEXT,
    originator_conversation_id TEXT,
    amount_kobo      BIGINT NOT NULL,
    phone            TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','completed','failed','timeout')),
    mpesa_receipt    TEXT,
    result_code      TEXT,
    result_desc      TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ
);

CREATE INDEX idx_mpesa_checkout  ON mpesa_requests(checkout_request_id);
CREATE INDEX idx_mpesa_user      ON mpesa_requests(user_id);

-- ── GRA Monitoring Log ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS gra_events (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    event_type  TEXT NOT NULL,
    user_id     UUID REFERENCES users(id),
    market_id   UUID REFERENCES markets(id),
    amount_kobo BIGINT,
    payload     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_gra_events_type    ON gra_events(event_type);
CREATE INDEX idx_gra_events_user    ON gra_events(user_id);
CREATE INDEX idx_gra_events_created ON gra_events(created_at DESC);

-- ── Responsible Gambling ─────────────────────────────────────
CREATE TABLE IF NOT EXISTS self_exclusions (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID NOT NULL REFERENCES users(id),
    period     TEXT NOT NULL CHECK (period IN ('30_days','90_days','permanent')),
    starts_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ends_at    TIMESTAMPTZ,  -- NULL for permanent
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Refresh Tokens ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id    UUID NOT NULL REFERENCES users(id),
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked    BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);
