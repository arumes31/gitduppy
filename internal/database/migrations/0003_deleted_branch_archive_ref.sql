-- 0003 deleted_branch_archive_ref
--
-- Pruned branches used to be protected by an archival ref derived only from the
-- branch name (refs/paperbin/heads/<branch>). Two deletion events of the same
-- branch therefore shared one ref, and expiring the older event deleted the ref
-- that still protected the newer one. Each event now gets its own ref
-- (refs/paperbin/events/<deleted_branches.id>), persisted in archive_ref.
--
-- GORM AutoMigrate adds the column on startup; this migration repeats it
-- idempotently so the schema does not depend on AutoMigrate alone. Existing rows
-- keep archive_ref = '' and continue to resolve to the legacy ref name, so no
-- archived commit loses its protection on upgrade.
--
-- Note on repositories.url: URLs with credentials embedded in their userinfo
-- are now rejected on create/update and redacted in every API response. Stored
-- rows are deliberately NOT rewritten here, because clones of those mirrors may
-- depend on the embedded secret. The server logs the IDs of affected
-- repositories at startup; operators should move the secret into the
-- repository's encrypted credentials and rotate it.

-- +goose Up
ALTER TABLE deleted_branches ADD COLUMN IF NOT EXISTS archive_ref varchar(1024) NOT NULL DEFAULT '';

-- +goose Down
-- Keep the column: dropping it would orphan the per-event refs that rows
-- created since this migration rely on for cleanup.
SELECT 1;
