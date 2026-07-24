package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrations = []string{
	"migrations/001_initial.sql",
}

func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
)`); err != nil {
		return fmt.Errorf("create conversation SQLite migration table: %w", err)
	}
	var current int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read conversation SQLite schema version: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("conversation SQLite schema version %d is newer than supported version %d", current, len(migrations))
	}
	for version := current + 1; version <= len(migrations); version++ {
		name := migrations[version-1]
		data, err := migrationFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read conversation SQLite migration %d: %w", version, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin conversation SQLite migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, string(data)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply conversation SQLite migration %d: %w", version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", version, time.Now().UTC().UnixNano()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record conversation SQLite migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit conversation SQLite migration %d: %w", version, err)
		}
	}
	return nil
}
