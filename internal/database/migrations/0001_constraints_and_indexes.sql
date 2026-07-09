-- 0001 constraints_and_indexes
--
-- Owns the integrity constraints and secondary indexes layered on top of the
-- base tables GORM AutoMigrate creates. Everything here is idempotent so it is
-- safe on fresh and pre-existing databases alike:
--   * CHECK constraints are added NOT VALID so existing (possibly legacy) rows
--     are never scanned and boot cannot fail on historical data; they are still
--     enforced for every subsequent INSERT/UPDATE.
--   * The ON DELETE CASCADE foreign keys are added under distinct *_cascade names
--     and coexist with the NO ACTION foreign keys GORM creates from the relation
--     tags. On a hard DELETE the CASCADE action removes the children first, after
--     which GORM's NO ACTION check (deferred to statement end) passes — so the
--     cascade is effective without having to predict/replace GORM's own names.
--     Added NOT VALID so pre-existing orphan rows do not block boot; ON DELETE
--     triggers still fire on a NOT VALID FK.
--   * Enum value lists below are exactly the values the Go code writes (plus '' for
--     repositories.visibility, which the create path leaves empty, and 'cloning'
--     for repositories.status, which dashboard_service treats as a legal status).

-- +goose Up

-- ---------------------------------------------------------------------------
-- CHECK constraints (enum-like columns)
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_users_role') THEN
        ALTER TABLE users
            ADD CONSTRAINT chk_users_role CHECK (role IN ('admin', 'user')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_repositories_status') THEN
        ALTER TABLE repositories
            ADD CONSTRAINT chk_repositories_status
            CHECK (status IN ('pending', 'cloning', 'success', 'failed')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_repositories_auth_type') THEN
        ALTER TABLE repositories
            ADD CONSTRAINT chk_repositories_auth_type
            CHECK (auth_type IN ('none', 'https', 'ssh', 'token')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_repositories_visibility') THEN
        ALTER TABLE repositories
            ADD CONSTRAINT chk_repositories_visibility
            CHECK (visibility IN ('', 'public', 'private')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_clone_jobs_status') THEN
        ALTER TABLE clone_jobs
            ADD CONSTRAINT chk_clone_jobs_status
            CHECK (status IN ('pending', 'running', 'success', 'failed', 'cancelled')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Foreign keys with ON DELETE CASCADE
-- (repositories use soft delete, so CASCADE only fires on a hard DELETE — the
--  paperbin purge path — which is the intended behavior.)
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_repository_tags_repository_cascade') THEN
        ALTER TABLE repository_tags
            ADD CONSTRAINT fk_repository_tags_repository_cascade
            FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_repository_tags_tag_cascade') THEN
        ALTER TABLE repository_tags
            ADD CONSTRAINT fk_repository_tags_tag_cascade
            FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_clone_jobs_repository_cascade') THEN
        ALTER TABLE clone_jobs
            ADD CONSTRAINT fk_clone_jobs_repository_cascade
            FOREIGN KEY (repository_id) REFERENCES repositories(id) ON DELETE CASCADE NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_clone_logs_clone_job_cascade') THEN
        ALTER TABLE clone_logs
            ADD CONSTRAINT fk_clone_logs_clone_job_cascade
            FOREIGN KEY (clone_job_id) REFERENCES clone_jobs(id) ON DELETE CASCADE NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_sessions_user_cascade') THEN
        ALTER TABLE sessions
            ADD CONSTRAINT fk_sessions_user_cascade
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_api_keys_user_cascade') THEN
        ALTER TABLE api_keys
            ADD CONSTRAINT fk_api_keys_user_cascade
            FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- Hot-path indexes
-- ---------------------------------------------------------------------------

-- api_keys.key_hash is looked up on every API-key auth and is logically unique.
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_hash_unique ON api_keys (key_hash);

-- repositories(status) alone (the existing composite index leads with is_active,
-- so it cannot serve the status-only GROUP BY / filter).
CREATE INDEX IF NOT EXISTS idx_repositories_status ON repositories (status);

-- ---------------------------------------------------------------------------
-- Trigram indexes for the repository list search (name LIKE '%..%' OR url LIKE
-- '%..%'). Guarded so this migration never fails when the pg_trgm extension is
-- unavailable (its creation needs elevated privileges and is best-effort in
-- createPerformanceIndexes). Same index names as createPerformanceIndexes, so at
-- most one physical index exists.
-- ---------------------------------------------------------------------------

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        CREATE INDEX IF NOT EXISTS idx_repositories_name_trgm
            ON repositories USING gin (name gin_trgm_ops);
        CREATE INDEX IF NOT EXISTS idx_repositories_url_trgm
            ON repositories USING gin (url gin_trgm_ops);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down

DROP INDEX IF EXISTS idx_repositories_status;
DROP INDEX IF EXISTS idx_api_keys_key_hash_unique;

ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS fk_api_keys_user_cascade;
ALTER TABLE sessions DROP CONSTRAINT IF EXISTS fk_sessions_user_cascade;
ALTER TABLE clone_logs DROP CONSTRAINT IF EXISTS fk_clone_logs_clone_job_cascade;
ALTER TABLE clone_jobs DROP CONSTRAINT IF EXISTS fk_clone_jobs_repository_cascade;
ALTER TABLE repository_tags DROP CONSTRAINT IF EXISTS fk_repository_tags_tag_cascade;
ALTER TABLE repository_tags DROP CONSTRAINT IF EXISTS fk_repository_tags_repository_cascade;

ALTER TABLE clone_jobs DROP CONSTRAINT IF EXISTS chk_clone_jobs_status;
ALTER TABLE repositories DROP CONSTRAINT IF EXISTS chk_repositories_visibility;
ALTER TABLE repositories DROP CONSTRAINT IF EXISTS chk_repositories_auth_type;
ALTER TABLE repositories DROP CONSTRAINT IF EXISTS chk_repositories_status;
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_users_role;
