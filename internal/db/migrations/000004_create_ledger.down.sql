DROP INDEX IF EXISTS idx_ledger_wallet_type;
DROP INDEX IF EXISTS idx_ledger_type;
DROP INDEX IF EXISTS idx_ledger_transaction_id;
DROP INDEX IF EXISTS idx_ledger_wallet_id;
DROP TABLE IF EXISTS ledgers CASCADE;
DROP TYPE IF EXISTS ledger_type;