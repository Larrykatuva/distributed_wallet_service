-- merchant_id was added in 000007; rows created before that have it NULL.
-- Backfill in two passes, then enforce NOT NULL once no row is missing it:
--   1. from the merchant of the initial debit (or credit) wallet;
--   2. from the merchant of any wallet in the persisted transfer plan.
UPDATE transactions
SET merchant_id = COALESCE(merchant_from, merchant_to)
WHERE merchant_id IS NULL;

UPDATE transactions t
SET merchant_id = (
    SELECT w.merchant_id
    FROM jsonb_array_elements(t.transfers) leg
    JOIN wallets w ON w.id = (leg->>'wallet_id')::uuid
    WHERE w.merchant_id IS NOT NULL
    LIMIT 1
)
WHERE t.merchant_id IS NULL AND jsonb_array_length(t.transfers) > 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM transactions WHERE merchant_id IS NULL) THEN
        ALTER TABLE transactions ALTER COLUMN merchant_id SET NOT NULL;
    ELSE
        RAISE NOTICE 'transactions.merchant_id left nullable: % row(s) have no resolvable merchant',
            (SELECT count(*) FROM transactions WHERE merchant_id IS NULL);
    END IF;
END$$;
