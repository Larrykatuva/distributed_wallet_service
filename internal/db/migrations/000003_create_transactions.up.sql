DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transaction_type') THEN
CREATE TYPE transaction_type AS ENUM (
            'transfer',
            'deposit',
            'withdrawal',
            'payment',
            'reversal',
            'adjustment'
        );
END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transaction_status') THEN
CREATE TYPE transaction_status AS ENUM (
            'pending',
            'processing',
            'success',
            'failed'
        );
END IF;
END$$;

CREATE TABLE IF NOT EXISTS transactions (
    id                UUID               NOT NULL DEFAULT gen_random_uuid(),

    -- Parties
    merchant_from     UUID,
    merchant_to       UUID,
    profile_from      UUID,
    profile_to        UUID,

    -- Display names
    sender_name       TEXT,
    receiver_name     TEXT,
    sender_merchant   TEXT,
    receiver_merchant TEXT,
    account_from      TEXT,
    account_to        TEXT,

    -- Wallets
    wallet_from       UUID,
    wallet_to         UUID,

    -- Financials
    amount            BIGINT             NOT NULL CHECK (amount > 0),
    currency          TEXT               NOT NULL DEFAULT 'KES',
    fee               BIGINT             NOT NULL DEFAULT 0 CHECK (fee >= 0),

    -- Classification
    type              transaction_type   NOT NULL,
    status            transaction_status NOT NULL DEFAULT 'pending',
    purpose           TEXT,

    -- References
    rrn               TEXT               NOT NULL,
    order_id          TEXT,
    provider_ref      TEXT,

    -- Description
    narration         TEXT,
    description       TEXT,

    -- Completion
    is_completed      BOOLEAN            NOT NULL DEFAULT FALSE,
    date_completed    TIMESTAMPTZ,

    created_at        TIMESTAMPTZ        NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ        NOT NULL DEFAULT NOW(),

    -- Partition key must be part of the primary key
    PRIMARY KEY (id, created_at)

    ) PARTITION BY RANGE (created_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_rrn
    ON transactions (rrn, created_at);

CREATE INDEX IF NOT EXISTS idx_transactions_order_id
    ON transactions (order_id, created_at DESC) WHERE order_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_transactions_provider_ref
    ON transactions (provider_ref, created_at DESC) WHERE provider_ref IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_transactions_status
    ON transactions (status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transactions_profile_from_or_to
    ON transactions (profile_from, profile_to, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transactions_wallet_from_or_to
    ON transactions (wallet_from, wallet_to, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_transactions_merchant_from_or_to
    ON transactions (merchant_from, merchant_to, created_at DESC);