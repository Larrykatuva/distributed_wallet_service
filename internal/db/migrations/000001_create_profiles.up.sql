DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'profile_type') THEN
CREATE TYPE profile_type AS ENUM ('individual', 'business');
END IF;
END$$;

CREATE TABLE IF NOT EXISTS profiles (
    id          UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    external_id TEXT          NOT NULL,
    type        profile_type  NOT NULL DEFAULT 'individual',
    full_name   TEXT,
    email       TEXT          NOT NULL,
    phone       TEXT,
    metadata    JSONB         NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
    );

CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_external_id ON profiles (external_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_profiles_email ON profiles (email);