package store

import (
	"context"
	"database/sql"
	"fmt"
)

// upSingleUser collapses the multi-user schema left behind by the legacy
// migrations 0013-0018 (roles, invites, requests, notifications,
// sessions.user_id, and the full users table) to the single-user shape: a slim
// users table plus the local owner row.
//
// The migration must be a safe no-op on fresh databases, where the edited 0013
// already created the slim users table. It branches on the live schema so the
// same append-only migration converges both paths to one shape.
func upSingleUser(ctx context.Context, tx *sql.Tx) error {
	legacy, err := hasColumn(ctx, tx, "users", "password_hash")
	if err != nil {
		return err
	}

	for _, table := range []string{"sessions", "invites", "requests", "notifications"} {
		if err := dropTable(ctx, tx, table); err != nil {
			return err
		}
	}

	if legacy {
		// users.role_id references roles, so the column must go before the table.
		for _, col := range []string{"role_id", "password_hash", "is_owner", "disabled", "updated_at", "last_seen"} {
			if err := dropColumn(ctx, tx, "users", col); err != nil {
				return err
			}
		}
		if err := dropTable(ctx, tx, "roles"); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO users (id, username, created_at) VALUES ('local', 'local', unixepoch())"); err != nil {
		return err
	}
	// Ensure device/sync/offline tables exist for databases that were already
	// at version 24 (old Go migration) and thus skipped the 0024 SQL migration.
	// This is idempotent for fresh databases that already applied 0024.
	if err := ensureDeviceSyncTables(ctx, tx); err != nil {
		return err
	}
	return nil
}

func ensureDeviceSyncTables(ctx context.Context, tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS device (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  is_server  INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL DEFAULT (unixepoch()),
  last_seen  INTEGER NOT NULL DEFAULT (unixepoch())
)`,
		`CREATE TABLE IF NOT EXISTS pairing_code (
  code       TEXT PRIMARY KEY,
  expires_at INTEGER NOT NULL,
  used_at    INTEGER,
  used_by_device_id TEXT REFERENCES device(id),
  created_at INTEGER NOT NULL DEFAULT (unixepoch())
)`,
		`CREATE TABLE IF NOT EXISTS sync_change (
  revision   INTEGER PRIMARY KEY AUTOINCREMENT,
  device_id  TEXT NOT NULL REFERENCES device(id),
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  field       TEXT NOT NULL,
  value_json  TEXT NOT NULL DEFAULT 'null',
  updated_at  INTEGER NOT NULL,
  created_at  INTEGER NOT NULL DEFAULT (unixepoch())
)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_change_revision ON sync_change(revision)`,
		`CREATE INDEX IF NOT EXISTS idx_sync_change_entity ON sync_change(entity_type, entity_id)`,
		`CREATE TABLE IF NOT EXISTS sync_cursor (
  device_id TEXT PRIMARY KEY REFERENCES device(id),
  revision  INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT (unixepoch())
)`,
		`CREATE TABLE IF NOT EXISTS offline_set (
  device_id   TEXT NOT NULL REFERENCES device(id),
  playlist_id TEXT NOT NULL REFERENCES synced_playlists(id) ON DELETE CASCADE,
  enabled     INTEGER NOT NULL DEFAULT 1,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (device_id, playlist_id)
)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("ensure device/sync tables: %w", err)
		}
	}
	return nil
}

func hasColumn(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", table, column).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("inspect %s.%s: %w", table, column, err)
	}
	return n > 0, nil
}

func dropTable(ctx context.Context, tx *sql.Tx, table string) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&n); err != nil {
		return fmt.Errorf("inspect table %s: %w", table, err)
	}
	if n == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE "+table); err != nil {
		return fmt.Errorf("drop table %s: %w", table, err)
	}
	return nil
}

func dropColumn(ctx context.Context, tx *sql.Tx, table, column string) error {
	exists, err := hasColumn(ctx, tx, table, column)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE "+table+" DROP COLUMN "+column); err != nil {
		return fmt.Errorf("drop column %s.%s: %w", table, column, err)
	}
	return nil
}
