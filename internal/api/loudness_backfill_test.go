package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

// pagedLibrary pages properly (the shared fake ignores offset, which a paging
// loop would spin on forever) and exposes local paths.
type pagedLibrary struct {
	*fakeLibrary
	ids   []string
	paths map[string]string
}

func (p *pagedLibrary) GetSongsBrowse(ctx context.Context, size, offset int) ([]core.Track, error) {
	if offset >= len(p.ids) {
		return nil, nil
	}
	end := offset + size
	if end > len(p.ids) {
		end = len(p.ids)
	}
	out := make([]core.Track, 0, end-offset)
	for _, id := range p.ids[offset:end] {
		out = append(out, core.Track{ID: id})
	}
	return out, nil
}

func (p *pagedLibrary) LocalTrackPath(id string) (string, bool) {
	path, ok := p.paths[id]
	return path, ok
}

func backfillServer(t *testing.T, lib *pagedLibrary) (*Server, *store.Store, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/backfill.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:     authSvc,
		Library:  lib,
		Loudness: st.Q(),
	})
	return srv, st, &http.Cookie{Name: sessionCookie, Value: tok}
}

func backfillStatus(t *testing.T, srv *Server, cookie *http.Cookie) backfillState {
	t.Helper()
	rec := do(t, srv, cookie, http.MethodGet, "/api/v1/library/loudness/backfill", "")
	var got backfillState
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func waitForIdle(t *testing.T, srv *Server, cookie *http.Cookie) backfillState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := backfillStatus(t, srv, cookie); !got.Running {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("backfill never finished")
	return backfillState{}
}

// A track that already has a cached measurement is skipped, so re-running the
// pass after a cancel costs nothing for the work already done.
func TestBackfillSkipsAlreadyMeasuredTracks(t *testing.T) {
	lib := &pagedLibrary{fakeLibrary: &fakeLibrary{}, ids: []string{"t1", "t2"}, paths: map[string]string{}}
	srv, st, cookie := backfillServer(t, lib)
	for _, id := range lib.ids {
		if err := st.Q().UpsertTrackLoudness(context.Background(), db.UpsertTrackLoudnessParams{
			TrackID: id, GainDb: -3, UpdatedAt: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if rec := do(t, srv, cookie, http.MethodPost, "/api/v1/library/loudness/backfill", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got := waitForIdle(t, srv, cookie)
	if got.Total != 2 || got.Skipped != 2 || got.Done != 0 {
		t.Fatalf("state = %+v, want both tracks skipped", got)
	}
}

// A track whose file this machine cannot see has nothing to measure. That is a
// skip, not a failure — an external library is a normal setup.
func TestBackfillSkipsTracksWithNoLocalFile(t *testing.T) {
	lib := &pagedLibrary{fakeLibrary: &fakeLibrary{}, ids: []string{"t1"}, paths: map[string]string{}}
	srv, _, cookie := backfillServer(t, lib)

	if rec := do(t, srv, cookie, http.MethodPost, "/api/v1/library/loudness/backfill", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	got := waitForIdle(t, srv, cookie)
	if got.Skipped != 1 || got.Failed != 0 {
		t.Fatalf("state = %+v, want a skip", got)
	}
}

// Measuring a whole library is expensive; starting a second pass on top of a
// running one would double the work for nothing.
func TestBackfillRefusesToRunTwice(t *testing.T) {
	lib := &pagedLibrary{fakeLibrary: &fakeLibrary{}, ids: []string{"t1"}, paths: map[string]string{}}
	srv, _, cookie := backfillServer(t, lib)

	srv.loudness.mu.Lock()
	srv.loudness.state = backfillState{Running: true}
	srv.loudness.mu.Unlock()

	if rec := do(t, srv, cookie, http.MethodPost, "/api/v1/library/loudness/backfill", ""); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// An external library exposes no filesystem paths, so there is nothing to
// measure and the endpoint says so rather than starting a pass that skips
// everything.
func TestBackfillUnavailableWithoutLocalFiles(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/backfill2.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts, Auth: authSvc, Library: &fakeLibrary{}, Loudness: st.Q()})
	cookie := &http.Cookie{Name: sessionCookie, Value: tok}

	if rec := do(t, srv, cookie, http.MethodPost, "/api/v1/library/loudness/backfill", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestBackfillCancelStops(t *testing.T) {
	lib := &pagedLibrary{fakeLibrary: &fakeLibrary{}, ids: []string{"t1"}, paths: map[string]string{}}
	srv, _, cookie := backfillServer(t, lib)

	if rec := do(t, srv, cookie, http.MethodPost, "/api/v1/library/loudness/backfill", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec := do(t, srv, cookie, http.MethodPost, "/api/v1/library/loudness/backfill/cancel", ""); rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d", rec.Code)
	}
	if got := waitForIdle(t, srv, cookie); got.Running {
		t.Fatalf("state = %+v, want stopped", got)
	}
}
