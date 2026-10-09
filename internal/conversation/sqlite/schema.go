package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

const schemaVersion = 2

//go:embed schema.sql
var schemaSQL string

// initializeSchema creates only the current schema in an empty database.
// An existing database must already use this schema; no conversion is attempted.
func initializeSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin conversation SQLite schema initialization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var current int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read conversation SQLite schema version: %w", err)
	}
	switch current {
	case schemaVersion:
		// Reopening the current schema must leave conversations unchanged.
	case 0:
		var tables int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
			return fmt.Errorf("inspect conversation SQLite schema: %w", err)
		}
		if tables != 0 {
			return fmt.Errorf("conversation SQLite database has an unrecognized schema")
		}
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("initialize conversation SQLite schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
			return fmt.Errorf("set conversation SQLite schema version: %w", err)
		}
	default:
		return fmt.Errorf("conversation SQLite schema version %d is unsupported, version %d is required: remove the database to start with an empty one", current, schemaVersion)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit conversation SQLite schema initialization: %w", err)
	}
	return nil
}
