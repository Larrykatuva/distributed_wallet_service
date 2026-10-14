-- A successful transaction moves to 'reversed' once its reversal (a separate
-- transaction of type 'reversal') settles successfully.
ALTER TYPE transaction_status ADD VALUE IF NOT EXISTS 'reversed';
