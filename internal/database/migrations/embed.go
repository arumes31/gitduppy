// Package migrations holds the embedded goose SQL migrations that own the
// database constraints, indexes and data fixes layered on top of the base
// tables/columns created by GORM AutoMigrate. Each migration is written
// idempotently (IF NOT EXISTS / guarded DO blocks) so it is safe on both fresh
// and pre-existing databases.
package migrations

import "embed"

// FS embeds the raw .sql migration files. It is handed to goose via
// goose.SetBaseFS in the database package.
//
//go:embed *.sql
var FS embed.FS
