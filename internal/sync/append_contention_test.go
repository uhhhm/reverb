package sync_test

import (
	"context"
	"testing"
	"time"

	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

// A background writer can hold SQLite's reserved lock while a playlist edit
// appends its change. The append must wait, not fail a read-to-write upgrade
// and leave the playlist edit out of the replicated log.
func TestAppendChangeWaitsForConcurrentWriter(t *testing.T) {
	st := newTestStoreSync(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	createDevice(t, st, "author", "author", 0)
	ss := syncpkg.NewSyncStore(st.Q())
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "UPDATE device SET name = name WHERE id = 'author'"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ss.AppendChange(ctx, "author", syncpkg.SyncChange{EntityType: "playlist", EntityID: "playlist", Field: "name", Value: "edited", UpdatedAt: 1000})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("append returned before concurrent writer released its lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rows, err := ss.ListSince(ctx, 0, 10)
	if err != nil || len(rows) != 1 || rows[0].Seq != 1 || rows[0].Value != "edited" {
		t.Fatalf("edit missing from log or author sequence skipped: %+v, %v", rows, err)
	}
}
