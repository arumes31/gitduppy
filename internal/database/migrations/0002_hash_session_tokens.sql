-- 0002 hash_session_tokens
--
-- Sessions now store the SHA-256 (hex, 64 chars) of the session token instead of
-- the raw 43-char token (see the Session model and pkg/crypto.HashToken). Two
-- things follow, both handled idempotently so this is safe on fresh and
-- pre-existing databases alike:
--   * The sessions.token column must be wide enough for the 64-char hash. GORM
--     AutoMigrate declares it as varchar(64) now, but on an upgraded database the
--     column may still be varchar(43); this migration widens it explicitly so a
--     hashed INSERT cannot fail with "value too long".
--   * Any rows written before the switch were keyed by the raw token and can
--     never match a hashed lookup again, so they are purged. The only visible
--     effect is that every user must log in once after deploy.

-- +goose Up

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'sessions'
          AND column_name = 'token'
          AND character_maximum_length IS NOT NULL
          AND character_maximum_length < 64
    ) THEN
        ALTER TABLE sessions ALTER COLUMN token TYPE varchar(64);
    END IF;
END $$;
-- +goose StatementEnd

-- Purge pre-hash sessions (raw-token keys that no longer match hashed lookups).
DELETE FROM sessions;

-- +goose Down

-- Clear hashed sessions (64-char hex values) so a rollback restarts with a
-- clean sessions table. Do NOT narrow the column back to varchar(43) — that
-- would take an exclusive lock and break fresh databases that were created
-- with GORM AutoMigrate's varchar(64) declaration.
DELETE FROM sessions WHERE LENGTH(token) = 64;
