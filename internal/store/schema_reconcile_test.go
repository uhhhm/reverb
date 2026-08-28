package store

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

// openAtVersion migrates a fresh DB up to (and including) migration v, leaving
// later migrations pending — the shape a partially-upgraded desktop DB has.
func openAtVersion(t *testing.T, v int64) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	migrateMu.Lock()
	defer migrateMu.Unlock()
	if err := st.prepareGoose(); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(st.sql, "migrations", v); err != nil {
		t.Fatal(err)
	}
	return st
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func columnExists(t *testing.T, db *sql.DB, table, col string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?`, table, col).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// TestMigrateRepairsStampedButMissingTable reproduces the reported failure: a
// desktop DB stamped through 0027 whose track_override table is absent, so
// 0029's ALTER aborts with "no such table: track_override". Migrate must heal
// the DB and complete.
func TestMigrateRepairsStampedButMissingTable(t *testing.T) {
	st := openAtVersion(t, 28)
	if _, err := st.DB().Exec(`DROP TABLE track_override`); err != nil {
		t.Fatal(err)
	}

	if err := st.Migrate(); err != nil {
		t.Fatalf("Migrate must repair the stamped-but-missing table, got: %v", err)
	}

	if !columnExists(t, st.DB(), "track_override", "catalog_id") {
		t.Error("track_override.catalog_id missing after repair + migration")
	}
	if !indexExists(t, st.DB(), "idx_track_override_catalog") {
		t.Error("idx_track_override_catalog missing after repair + migration")
	}
}

// TestReconcileSchemaRestoresMissingObjects covers drift discovered after the
// creating migration is already stamped, where goose will never re-run it.
func TestReconcileSchemaRestoresMissingObjects(t *testing.T) {
	st := openMigrated(t)
	for _, stmt := range []string{
		`DROP TABLE track_override`,
		`DROP TABLE file_manifest`,
		`DROP INDEX idx_sync_change_device_seq`,
	} {
		if _, err := st.DB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	n, err := st.reconcileSchema()
	if err != nil {
		t.Fatalf("reconcileSchema: %v", err)
	}
	if n == 0 {
		t.Fatal("expected repairs, got none")
	}

	if !tableExists(t, st.DB(), "file_manifest") {
		t.Error("file_manifest not restored")
	}
	if !indexExists(t, st.DB(), "idx_sync_change_device_seq") {
		t.Error("idx_sync_change_device_seq not restored")
	}
	// track_override is created by 0027 and altered by 0029: the reconciler must
	// replay both, not just the CREATE.
	if !columnExists(t, st.DB(), "track_override", "catalog_id") {
		t.Error("track_override restored without 0029's catalog_id column")
	}
}

// TestReconcileSchemaRestoresMissingColumn covers a table that survived but
// lost a column added by a later, already-stamped migration.
func TestReconcileSchemaRestoresMissingColumn(t *testing.T) {
	st := openMigrated(t)
	// The index over catalog_id must go first; SQLite refuses to drop a column
	// an index still references.
	for _, stmt := range []string{
		`DROP INDEX idx_track_override_catalog`,
		`ALTER TABLE track_override DROP COLUMN catalog_id`,
	} {
		if _, err := st.DB().Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := st.reconcileSchema(); err != nil {
		t.Fatalf("reconcileSchema: %v", err)
	}
	if !columnExists(t, st.DB(), "track_override", "catalog_id") {
		t.Error("catalog_id not restored")
	}
	if !indexExists(t, st.DB(), "idx_track_override_catalog") {
		t.Error("idx_track_override_catalog not restored")
	}
}

// TestReconcileSchemaNoOpOnHealthyDB is the safety property: a DB that matches
// its stamped migrations must not be touched at all.
func TestReconcileSchemaNoOpOnHealthyDB(t *testing.T) {
	st := openMigrated(t)
	n, err := st.reconcileSchema()
	if err != nil {
		t.Fatalf("reconcileSchema: %v", err)
	}
	if n != 0 {
		t.Errorf("healthy DB must need no repairs, got %d", n)
	}
}

// TestReconcileSchemaKeepsDroppedTablesDropped guards the Go-migration blind
// spot: 0001 creates `sessions` and the Go migration 0025 drops it, so a
// SQL-only replay would resurrect it.
func TestReconcileSchemaKeepsDroppedTablesDropped(t *testing.T) {
	st := openMigrated(t)
	if tableExists(t, st.DB(), "sessions") {
		t.Fatal("precondition: sessions should already be dropped by 0025")
	}
	if _, err := st.reconcileSchema(); err != nil {
		t.Fatalf("reconcileSchema: %v", err)
	}
	if tableExists(t, st.DB(), "sessions") {
		t.Error("reconciler resurrected a table that migration 0025 drops")
	}
}

// TestReconcileSchemaIgnoresPendingMigrations ensures the reconciler restores
// only what the stamped version claims, leaving pending work to goose.
func TestReconcileSchemaIgnoresPendingMigrations(t *testing.T) {
	st := openAtVersion(t, 30)
	if _, err := st.reconcileSchema(); err != nil {
		t.Fatalf("reconcileSchema: %v", err)
	}
	if tableExists(t, st.DB(), "p2p_peer") {
		t.Error("reconciler created p2p_peer from unapplied migration 0031")
	}
}

// TestReconcileSchemaSkipsFreshDB: with no goose table there is nothing to
// reconcile against, and goose.Up owns the whole schema.
func TestReconcileSchemaSkipsFreshDB(t *testing.T) {
	st, err := Open(t.TempDir() + "/fresh.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	n, err := st.reconcileSchema()
	if err != nil {
		t.Fatalf("reconcileSchema on fresh DB: %v", err)
	}
	if n != 0 {
		t.Errorf("fresh DB must need no repairs, got %d", n)
	}
}
