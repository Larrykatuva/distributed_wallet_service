-- The merchant on whose behalf the transaction is initiated.
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS merchant_id UUID;

CREATE INDEX IF NOT EXISTS idx_transactions_merchant_id
    ON transactions (merchant_id, created_at DESC) WHERE merchant_id IS NOT NULL;
