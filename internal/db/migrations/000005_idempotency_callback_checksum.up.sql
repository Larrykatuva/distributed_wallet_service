-- Idempotency: the WalletActor looks a redelivered message up by key before
-- applying it. A plain index is enough because the actor serialises writes
-- per wallet; a UNIQUE index would have to include the partition key anyway.
ALTER TABLE ledgers ADD COLUMN IF NOT EXISTS idempotency_key UUID;

CREATE INDEX IF NOT EXISTS idx_ledger_wallet_idempotency
    ON ledgers (wallet_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Completion webhook target, supplied on initiation.
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS callback_url TEXT;

-- Stamp legacy wallets with a checksum so integrity checks apply from now on.
-- Must match actors.ComputeChecksum: sha256("id:actual:available:processing:version").
UPDATE wallets
SET checksum = encode(
        sha256(convert_to(
            id::text || ':' || actual_balance || ':' || available_balance || ':' || processing_balance || ':' || version,
            'UTF8')),
        'hex')
WHERE checksum = '';
