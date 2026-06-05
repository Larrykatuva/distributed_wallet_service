DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'ledger_type') THEN
CREATE TYPE ledger_type AS ENUM ('debit', 'credit');
END IF;
END$$;

CREATE TABLE IF NOT EXISTS ledgers (
   id              BIGSERIAL   NOT NULL,
   wallet_id       UUID        NOT NULL,
   transaction_id  UUID        NOT NULL,
   type            ledger_type NOT NULL,
   purpose         TEXT,
   amount          BIGINT      NOT NULL CHECK (amount > 0),
    currency        TEXT        NOT NULL DEFAULT 'KES',
    initial_balance BIGINT      NOT NULL,
    updated_balance BIGINT      NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (id, created_at)

    ) PARTITION BY RANGE (created_at);

CREATE INDEX IF NOT EXISTS idx_ledger_wallet_id
    ON ledgers (wallet_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ledger_transaction_id
    ON ledgers (transaction_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ledger_type
    ON ledgers (type, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ledger_wallet_type
    ON ledgers (wallet_id, type, created_at DESC);