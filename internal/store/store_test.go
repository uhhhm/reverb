package store

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/store/db"
)

func openMigrated(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/s.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestMigrateFromLegacyMultiUserSchema(t *testing.T) {
	// Reproduces a database created by the old multi-user migration set
	// (0013-0018): full users table, roles/invites/requests/notifications,
	// sessions.user_id, and the FK columns. Upgrading must collapse it to the
	// single-user shape without breaking the FK targets for existing data.
	dbpath := t.TempDir() + "/legacy.db"
	raw, err := sql.Open("sqlite", dbpath+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	const legacy = `
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE sessions (id TEXT PRIMARY KEY, token_hash TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL DEFAULT (unixepoch()), expires_at INTEGER NOT NULL, last_seen INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE download_jobs (id TEXT PRIMARY KEY, dedup_key TEXT NOT NULL, request_json TEXT NOT NULL DEFAULT '{}', downloader_name TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'queued', progress INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', output_path TEXT NOT NULL DEFAULT '', library_track_id TEXT, priority INTEGER NOT NULL DEFAULT 0, requested_by TEXT, attempts INTEGER NOT NULL DEFAULT 0, cover_art_id TEXT, downloader_ref TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL DEFAULT (unixepoch()), started_at INTEGER, finished_at INTEGER);
CREATE INDEX idx_download_jobs_dedup_active ON download_jobs (dedup_key, status);
CREATE TABLE synced_playlists (id TEXT PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE roles (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, is_system INTEGER NOT NULL DEFAULT 0, capabilities TEXT NOT NULL DEFAULT '[]', created_at INTEGER NOT NULL DEFAULT (unixepoch()), updated_at INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE, password_hash TEXT NOT NULL, role_id TEXT NOT NULL REFERENCES roles(id), is_owner INTEGER NOT NULL DEFAULT 0, disabled INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL DEFAULT (unixepoch()), updated_at INTEGER NOT NULL DEFAULT (unixepoch()), last_seen INTEGER);
CREATE TABLE invites (id TEXT PRIMARY KEY, code TEXT NOT NULL UNIQUE, role_id TEXT REFERENCES roles(id), created_by TEXT REFERENCES users(id), expires_at INTEGER, used_by TEXT REFERENCES users(id), used_at INTEGER, created_at INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE requests (id TEXT PRIMARY KEY, requested_by TEXT NOT NULL REFERENCES users(id), source TEXT NOT NULL, external_id TEXT NOT NULL, title TEXT NOT NULL, artist TEXT NOT NULL, status TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT (unixepoch()));
CREATE TABLE notifications (id TEXT PRIMARY KEY, user_id TEXT NOT NULL, type TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, read INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL);
ALTER TABLE sessions ADD COLUMN user_id TEXT REFERENCES users(id);
ALTER TABLE download_jobs ADD COLUMN initiated_by TEXT REFERENCES users(id);
ALTER TABLE synced_playlists ADD COLUMN owner_user_id TEXT REFERENCES users(id);
INSERT INTO roles (id, name, is_system) VALUES ('role-admin', 'Admin', 1);
INSERT INTO users (id, username, password_hash, role_id, is_owner) VALUES ('u1', 'admin', 'hash', 'role-admin', 1);
INSERT INTO download_jobs (id, dedup_key, request_json, initiated_by) VALUES ('dj1', 'dk1', '{"title":"legacy"}', 'u1');
`
	if _, err := raw.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY AUTOINCREMENT, version_id INTEGER NOT NULL, is_applied INTEGER NOT NULL, tstamp TIMESTAMP DEFAULT (datetime('now')))"); err != nil {
		t.Fatal(err)
	}
	// A real install that ran the old migration set has every version 1-18 recorded.
	for v := 1; v <= 18; v++ {
		if _, err := raw.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", v); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	q := st.Q()
	ctx := context.Background()

	// The local owner exists (FK target for attribution columns), and legacy
	// rows survive the collapse.
	u, err := q.GetUserByID(ctx, "local")
	if err != nil {
		t.Fatalf("local user missing after upgrade: %v", err)
	}
	if u.Username != "local" {
		t.Fatalf("local username = %q", u.Username)
	}

	// The users table is slim: legacy columns are gone.
	cols, err := tableColumns(ctx, st.DB(), "users")
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"password_hash", "role_id", "is_owner", "disabled", "updated_at", "last_seen"} {
		if cols[gone] {
			t.Errorf("legacy column users.%s survived the upgrade", gone)
		}
	}
	for _, gone := range []string{"sessions", "invites", "requests", "notifications", "roles"} {
		if _, err := st.DB().QueryContext(ctx, "SELECT 1 FROM "+gone+" LIMIT 1"); err == nil {
			t.Errorf("legacy table %s survived the upgrade", gone)
		}
	}

	// FK enforcement on the attribution columns still works, now targeting local.
	if err := q.InsertDownloadJob(ctx, db.InsertDownloadJobParams{
		ID: "dj2", DedupKey: "dk2", RequestJson: `{"title":"x"}`, DownloaderName: "spotdl",
		Status: "queued", InitiatedBy: sql.NullString{String: "missing", Valid: true},
	}); err == nil {
		t.Fatal("insert attributed to missing user should violate the FK")
	}
	if err := q.InsertDownloadJob(ctx, db.InsertDownloadJobParams{
		ID: "dj2", DedupKey: "dk2", RequestJson: `{"title":"x"}`, DownloaderName: "spotdl",
		Status: "queued", InitiatedBy: sql.NullString{String: "local", Valid: true},
	}); err != nil {
		t.Fatalf("insert attributed to local should succeed: %v", err)
	}

	// The legacy job's attribution row still resolves (its user row survived).
	var initBy sql.NullString
	if err := st.DB().QueryRowContext(ctx, "SELECT initiated_by FROM download_jobs WHERE id = 'dj1'").Scan(&initBy); err != nil {
		t.Fatal(err)
	}
	if !initBy.Valid || initBy.String != "u1" {
		t.Fatalf("legacy job attribution lost: %+v", initBy)
	}
}

// tableColumns returns the column names of table.
func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

func TestUsersRoundTrip(t *testing.T) {
	st := openMigrated(t) // existing helper in this file
	ctx := context.Background()
	q := st.Q()
	if err := q.CreateUser(ctx, db.CreateUserParams{ID: "u1", Username: "alice"}); err != nil {
		t.Fatal(err)
	}
	u, err := q.GetUserByUsername(ctx, "ALICE") // NOCASE
	if err != nil || u.ID != "u1" {
		t.Fatalf("case-insensitive lookup failed: %v %+v", err, u)
	}
	n, _ := q.CountUsers(ctx)
	if n != 2 {
		t.Fatalf("want 2 users (seeded local + created), got %d", n)
	}
}

func TestLibraryVersionDefaultsToOne(t *testing.T) {
	st := openMigrated(t)
	v, err := st.LibraryVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("library_version = %d, want 1", v)
	}
}

func TestLibraryVersionSetAndGet(t *testing.T) {
	st := openMigrated(t)
	if err := st.Q().SetLibraryVersion(context.Background(), "5"); err != nil {
		t.Fatal(err)
	}
	v, err := st.LibraryVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if v != 5 {
		t.Fatalf("library_version = %d, want 5", v)
	}
}

func TestSetAndGetLibraryVersion(t *testing.T) {
	st, err := Open(t.TempDir() + "/lv.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.SetLibraryVersion(ctx, 7); err != nil {
		t.Fatal(err)
	}
	v, err := st.LibraryVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != 7 {
		t.Fatalf("library_version = %d, want 7", v)
	}
}

func TestDownloadJobRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir() + "/dj.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	q := st.Q()

	if err := q.InsertDownloadJob(ctx, db.InsertDownloadJobParams{
		ID: "j1", DedupKey: "dk1", RequestJson: `{"title":"Song"}`, DownloaderName: "spotdl",
		Status: "queued", Progress: 0, Error: "", OutputPath: "",
		LibraryTrackID: sql.NullString{}, Priority: 0, RequestedBy: sql.NullString{}, Attempts: 0,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := q.GetDownloadJob(ctx, "j1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DedupKey != "dk1" || got.Status != "queued" || got.DownloaderName != "spotdl" {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	// Dedup-join lookup finds the active (queued) job.
	active, err := q.GetActiveDownloadJobByDedup(ctx, "dk1")
	if err != nil {
		t.Fatalf("active lookup: %v", err)
	}
	if active.ID != "j1" {
		t.Fatalf("active = %q, want j1", active.ID)
	}

	// Move to running, then completed; finished_at must be set.
	if err := q.UpdateDownloadJobStatus(ctx, db.UpdateDownloadJobStatusParams{
		Status: "running", ID: "j1",
	}); err != nil {
		t.Fatalf("status running: %v", err)
	}
	if err := q.UpdateDownloadJobStatus(ctx, db.UpdateDownloadJobStatusParams{
		Status: "completed", ID: "j1",
	}); err != nil {
		t.Fatalf("status completed: %v", err)
	}
	done, _ := q.GetDownloadJob(ctx, "j1")
	if !done.FinishedAt.Valid || !done.StartedAt.Valid {
		t.Fatalf("started/finished not set: %+v", done)
	}

	// A completed job is no longer "active" for dedup-join.
	if _, err := q.GetActiveDownloadJobByDedup(ctx, "dk1"); err != sql.ErrNoRows {
		t.Fatalf("completed job should not be active, err=%v", err)
	}
}

func TestMatchCacheUpsertPositiveAndNegative(t *testing.T) {
	st := openMigrated(t)
	ctx := context.Background()
	q := st.Q()

	// Positive match.
	if err := q.UpsertMatchCache(ctx, db.UpsertMatchCacheParams{
		Source: "spotify", ExternalID: "sp1",
		LibraryTrackID: sql.NullString{String: "t1", Valid: true},
		Method:         "isrc", Confidence: 1, Isrc: "USX1", Mbid: "", DurationMs: 210000, LibraryVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetMatchCache(ctx, db.GetMatchCacheParams{Source: "spotify", ExternalID: "sp1"})
	if err != nil {
		t.Fatal(err)
	}
	if !row.LibraryTrackID.Valid || row.LibraryTrackID.String != "t1" || row.Method != "isrc" {
		t.Fatalf("positive row: %+v", row)
	}

	// Negative match (library_track_id NULL).
	if err := q.UpsertMatchCache(ctx, db.UpsertMatchCacheParams{
		Source: "spotify", ExternalID: "sp2",
		LibraryTrackID: sql.NullString{Valid: false},
		Method:         "none", Confidence: 0, LibraryVersion: 1,
	}); err != nil {
		t.Fatal(err)
	}
	neg, err := q.GetMatchCache(ctx, db.GetMatchCacheParams{Source: "spotify", ExternalID: "sp2"})
	if err != nil {
		t.Fatal(err)
	}
	if neg.LibraryTrackID.Valid {
		t.Fatalf("negative row should have NULL library_track_id: %+v", neg)
	}

	// DeleteBySource clears both.
	if err := q.DeleteMatchCacheBySource(ctx, "spotify"); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetMatchCache(ctx, db.GetMatchCacheParams{Source: "spotify", ExternalID: "sp1"}); err == nil {
		t.Fatal("expected ErrNoRows after delete")
	}
}

func TestAlbumCoverageRoundTrip(t *testing.T) {
	st := openMigrated(t)
	q := st.Q()
	ctx := context.Background()
	if err := q.UpsertAlbumCoverage(ctx, db.UpsertAlbumCoverageParams{
		Source: "spotify", ExternalAlbumID: "AL", CoverageJson: `{"state":"full"}`,
		LibraryAlbumID: "L1", FetchedAt: 123,
	}); err != nil {
		t.Fatal(err)
	}
	row, err := q.GetAlbumCoverage(ctx, db.GetAlbumCoverageParams{Source: "spotify", ExternalAlbumID: "AL"})
	if err != nil || row.CoverageJson != `{"state":"full"}` || row.LibraryAlbumID != "L1" {
		t.Fatalf("round-trip failed: %+v err=%v", row, err)
	}
}

func TestSyncedPlaylistRoundTrip(t *testing.T) {
	st := openMigrated(t)
	q := st.Q()
	ctx := context.Background()
	row, err := q.UpsertSyncedPlaylist(ctx, db.UpsertSyncedPlaylistParams{
		ID: "sp1", Source: "spotify", ExternalID: "ext1", Name: "Chill",
		CoverUrl: "http://img", TracksJson: `[]`, CreatedAt: 100,
	})
	if err != nil || row.Name != "Chill" {
		t.Fatalf("upsert: %+v err=%v", row, err)
	}
	// Upsert again with same (source, external_id) updates, not duplicates.
	if _, err := q.UpsertSyncedPlaylist(ctx, db.UpsertSyncedPlaylistParams{
		ID: "sp1", Source: "spotify", ExternalID: "ext1", Name: "Renamed", TracksJson: `[]`, CreatedAt: 100,
	}); err != nil {
		t.Fatal(err)
	}
	all, _ := q.ListSyncedPlaylists(ctx)
	if len(all) != 1 || all[0].Name != "Renamed" {
		t.Fatalf("want 1 row 'Renamed', got %+v", all)
	}
}

func TestAdapterInstanceCRUD(t *testing.T) {
	st, err := Open(t.TempDir() + "/ai.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	q := st.Q()

	id := uuid.NewString()
	if err := q.CreateAdapterInstance(ctx, db.CreateAdapterInstanceParams{
		ID: id, Type: "search", Name: "spotify", Enabled: 1, Priority: 0,
		ConfigJson: `{"client_id":"abc","client_secret":"shh"}`,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := q.GetAdapterInstance(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "spotify" || got.Enabled != 1 {
		t.Fatalf("get mismatch: %+v", got)
	}

	if err := q.UpdateAdapterInstance(ctx, db.UpdateAdapterInstanceParams{
		Name: "spotify", Enabled: 1, Priority: 5, ConfigJson: `{"client_id":"new"}`, ID: id,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = q.GetAdapterInstance(ctx, id)
	if got.Priority != 5 || got.ConfigJson != `{"client_id":"new"}` {
		t.Fatalf("update did not persist: %+v", got)
	}

	if err := q.SetAdapterInstanceEnabled(ctx, db.SetAdapterInstanceEnabledParams{Enabled: 0, ID: id}); err != nil {
		t.Fatalf("set-enabled: %v", err)
	}
	got, _ = q.GetAdapterInstance(ctx, id)
	if got.Enabled != 0 {
		t.Fatalf("enabled not toggled: %+v", got)
	}

	if err := q.SetAdapterInstancePriority(ctx, db.SetAdapterInstancePriorityParams{Priority: 9, ID: id}); err != nil {
		t.Fatalf("set-priority: %v", err)
	}
	got, _ = q.GetAdapterInstance(ctx, id)
	if got.Priority != 9 {
		t.Fatalf("priority not set: %+v", got)
	}

	if err := q.DeleteAdapterInstance(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := q.GetAdapterInstance(ctx, id); err == nil {
		t.Fatal("expected error getting a deleted instance")
	}
}
