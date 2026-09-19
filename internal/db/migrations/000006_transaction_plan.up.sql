-- The transfer plan (legs + idempotency keys) is persisted with the
-- transaction so a lost orchestrator can be recovered from the database:
-- progress is reconstructed by matching ledger rows to the plan's keys.
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS transfers JSONB NOT NULL DEFAULT '[]';

-- Stuck-transaction sweeps look for incomplete rows that stopped moving.
CREATE INDEX IF NOT EXISTS idx_transactions_incomplete
    ON transactions (updated_at)
    WHERE is_completed = FALSE;
