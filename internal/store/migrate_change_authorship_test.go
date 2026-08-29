package store

import (
	"context"
	"testing"
)

// seedAuthorshipDB builds a database with a server device, a local device, and
// changes authored under each.
func seedAuthorshipDB(t *testing.T, serverChanges, localChanges int) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/authorship.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	d := st.DB()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT OR REPLACE INTO device (id, name, token_hash, is_server) VALUES ('dev_server','server','h_server',1)`)
	exec(`INSERT OR REPLACE INTO device (id, name, token_hash, is_server) VALUES ('dev_local','local','h_local',0)`)
	exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('server_device_id','dev_server')`)
	exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('local_device_id','dev_local')`)
	exec(`DELETE FROM sync_change`)
	exec(`DELETE FROM sync_vector`)
	for i := 1; i <= localChanges; i++ {
		exec(`INSERT INTO sync_change (device_id, entity_type, entity_id, field, value_json, updated_at, hlc, seq, sig)
		      VALUES ('dev_local','track','trk_local','title','"L"',1000,?,?,'sig_local')`, 100+i, i)
	}
	for i := 1; i <= serverChanges; i++ {
		exec(`INSERT INTO sync_change (device_id, entity_type, entity_id, field, value_json, updated_at, hlc, seq, sig)
		      VALUES ('dev_server','track','trk_server','title','"S"',2000,?,?,'')`, 200+i, i)
	}
	if serverChanges > 0 {
		exec(`INSERT INTO sync_vector (device_id, seq, hlc) VALUES ('dev_server', ?, ?)`, serverChanges, 200+serverChanges)
	}
	if localChanges > 0 {
		exec(`INSERT INTO sync_vector (device_id, seq, hlc) VALUES ('dev_local', ?, ?)`, localChanges, 100+localChanges)
	}
	return st
}

func runAuthorshipMigration(t *testing.T, st *Store) {
	t.Helper()
	tx, err := st.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := upChangeAuthorship(context.Background(), tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("upChangeAuthorship: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func countFor(t *testing.T, st *Store, deviceID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sync_change WHERE device_id = ?`, deviceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Server-authored changes can never be signed and are refused forever by peers,
// so they must be re-attributed to the local device.
func TestChangeAuthorshipReattributesServerChanges(t *testing.T) {
	st := seedAuthorshipDB(t, 3, 0)
	runAuthorshipMigration(t, st)

	if n := countFor(t, st, "dev_server"); n != 0 {
		t.Errorf("%d change(s) still authored by the server device", n)
	}
	if n := countFor(t, st, "dev_local"); n != 3 {
		t.Errorf("local device has %d change(s), want 3", n)
	}
	rows, err := st.DB().Query(`SELECT seq, sig FROM sync_change WHERE device_id='dev_local' ORDER BY revision`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := int64(1)
	for rows.Next() {
		var seq int64
		var sig string
		if err := rows.Scan(&seq, &sig); err != nil {
			t.Fatal(err)
		}
		if seq != want {
			t.Errorf("seq = %d, want %d", seq, want)
		}
		// Cleared so BackfillLocalSignatures re-signs against the new seq.
		if sig != "" {
			t.Errorf("seq %d kept signature %q; the seq it covers has changed", seq, sig)
		}
		want++
	}
}

// The moved rows must not collide with seq numbers the local device already
// used: seq is per-author and the log is ordered by it.
func TestChangeAuthorshipRenumbersAfterExistingLocalSeq(t *testing.T) {
	st := seedAuthorshipDB(t, 2, 5)
	runAuthorshipMigration(t, st)

	if n := countFor(t, st, "dev_local"); n != 7 {
		t.Fatalf("local device has %d change(s), want 7", n)
	}
	rows, err := st.DB().Query(`SELECT seq FROM sync_change WHERE device_id='dev_local' AND entity_id='trk_server' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := []int64{}
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			t.Fatal(err)
		}
		got = append(got, seq)
	}
	if len(got) != 2 || got[0] != 6 || got[1] != 7 {
		t.Errorf("moved seqs = %v, want [6 7] (after the local device's existing 1..5)", got)
	}
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM sync_vector WHERE device_id='dev_server'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("server device still has a vector entry; peers would be offered a high-water mark for an author with no changes")
	}
	var seq int64
	if err := st.DB().QueryRow(`SELECT seq FROM sync_vector WHERE device_id='dev_local'`).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	if seq != 7 {
		t.Errorf("local vector seq = %d, want 7", seq)
	}
}

// A peer's own server-authored rows, relayed to us, belong to that peer.
func TestChangeAuthorshipLeavesPeerRowsAlone(t *testing.T) {
	st := seedAuthorshipDB(t, 1, 0)
	if _, err := st.DB().Exec(`INSERT OR REPLACE INTO device (id, name, token_hash, is_server) VALUES ('dev_peer','peer','h_peer',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`INSERT INTO sync_change (device_id, entity_type, entity_id, field, value_json, updated_at, hlc, seq, sig)
	                           VALUES ('dev_peer','track','trk_peer','title','"P"',3000,300,1,'sig_peer')`); err != nil {
		t.Fatal(err)
	}
	runAuthorshipMigration(t, st)

	if n := countFor(t, st, "dev_peer"); n != 1 {
		t.Errorf("peer-authored rows = %d, want 1 left untouched", n)
	}
}

// A fresh database, and one where the two identities coincide, must be no-ops.
func TestChangeAuthorshipNoOpCases(t *testing.T) {
	st := seedAuthorshipDB(t, 0, 0)
	if _, err := st.DB().Exec(`DELETE FROM settings WHERE key IN ('server_device_id','local_device_id')`); err != nil {
		t.Fatal(err)
	}
	runAuthorshipMigration(t, st) // must not error with the settings absent

	st2 := seedAuthorshipDB(t, 2, 0)
	if _, err := st2.DB().Exec(`UPDATE settings SET value='dev_server' WHERE key='local_device_id'`); err != nil {
		t.Fatal(err)
	}
	runAuthorshipMigration(t, st2)
	if n := countFor(t, st2, "dev_server"); n != 2 {
		t.Errorf("changes were moved when both identities are the same device: %d left, want 2", n)
	}
}
