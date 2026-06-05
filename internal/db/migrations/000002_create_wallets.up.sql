DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'wallet_status') THEN
CREATE TYPE wallet_status AS ENUM ('active', 'suspended', 'frozen', 'closed');
END IF;
END$$;

CREATE SEQUENCE IF NOT EXISTS wallet_number_seq START 100000;

CREATE TABLE IF NOT EXISTS wallets (
    id                  UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    number              TEXT          NOT NULL DEFAULT 'W' || NEXTVAL('wallet_number_seq'),
    profile_id          UUID          NOT NULL REFERENCES profiles(id),
    merchant_id         UUID          REFERENCES profiles(id),
    status              wallet_status NOT NULL DEFAULT 'active',
    currency            TEXT          NOT NULL DEFAULT 'KES',

    available_balance   BIGINT        NOT NULL DEFAULT 0 CHECK (available_balance >= 0),
    processing_balance  BIGINT        NOT NULL DEFAULT 0 CHECK (processing_balance >= 0),
    actual_balance      BIGINT        NOT NULL DEFAULT 0 CHECK (actual_balance >= 0),

    checksum            TEXT          NOT NULL DEFAULT '',
    version             BIGINT        NOT NULL DEFAULT 0,

    created_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW()
    );

ALTER TABLE wallets SET (fillfactor = 70);

ALTER TABLE wallets SET (
    autovacuum_vacuum_scale_factor = 0.01,
    autovacuum_analyze_scale_factor = 0.005,
    autovacuum_vacuum_cost_delay = 2
    );

CREATE UNIQUE INDEX IF NOT EXISTS idx_wallets_number ON wallets (number);
CREATE INDEX IF NOT EXISTS idx_wallets_profile_currency ON wallets (profile_id, currency);
CREATE INDEX IF NOT EXISTS idx_wallets_profile_id ON wallets (profile_id);
CREATE INDEX IF NOT EXISTS idx_wallets_merchant_id ON wallets (merchant_id);
CREATE INDEX IF NOT EXISTS idx_wallets_status ON wallets (status);
CREATE INDEX IF NOT EXISTS idx_wallets_currency ON wallets (currency);