-- Postgres cannot drop an enum value, so the type is rebuilt without it.
-- Reversed rows fall back to 'success'; their reversal rows still record the undo.
ALTER TABLE transactions ALTER COLUMN status DROP DEFAULT;
ALTER TABLE transactions ALTER COLUMN status TYPE TEXT;
UPDATE transactions SET status = 'success' WHERE status = 'reversed';

DROP TYPE transaction_status;
CREATE TYPE transaction_status AS ENUM (
    'pending',
    'processing',
    'success',
    'failed'
);

ALTER TABLE transactions ALTER COLUMN status TYPE transaction_status USING status::transaction_status;
ALTER TABLE transactions ALTER COLUMN status SET DEFAULT 'pending';
