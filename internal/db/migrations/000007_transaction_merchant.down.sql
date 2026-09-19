DROP INDEX IF EXISTS idx_transactions_merchant_id;
ALTER TABLE transactions DROP COLUMN IF EXISTS merchant_id;
