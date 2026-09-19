DROP INDEX IF EXISTS idx_ledger_wallet_idempotency;
ALTER TABLE ledgers DROP COLUMN IF EXISTS idempotency_key;
ALTER TABLE transactions DROP COLUMN IF EXISTS callback_url;
