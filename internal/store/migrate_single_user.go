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

	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO users (id, username, created_at) VALUES ('local', 'local', unixepoch())")
	return err
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
