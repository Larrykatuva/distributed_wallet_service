DROP INDEX IF EXISTS idx_transactions_incomplete;
ALTER TABLE transactions DROP COLUMN IF EXISTS transfers;
