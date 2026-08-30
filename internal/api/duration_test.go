package api

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

func durationServer(t *testing.T, lib *pagedLibrary) (*Server, *store.Store, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/duration.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{Auth: authSvc, Library: lib, Duration: st.Q()})
	return srv, st, &http.Cookie{Name: sessionCookie, Value: tok}
}

// toneFile writes a WAV of a known length for the library to point at.
func toneFile(t *testing.T, seconds string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi",
		"-i", "sine=frequency=440:duration="+seconds, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate tone: %v\n%s", err, out)
	}
	return path
}

func TestTrackDurationMeasuresAndCaches(t *testing.T) {
	path := toneFile(t, "2")
	lib := &pagedLibrary{fakeLibrary: &fakeLibrary{}, ids: []string{"t1"}, paths: map[string]string{"t1": path}}
	srv, st, cookie := durationServer(t, lib)

	rec := do(t, srv, cookie, http.MethodGet, "/api/v1/library/track/t1/duration", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		DurationMs int64 `json:"durationMs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DurationMs < 1950 || body.DurationMs > 2050 {
		t.Fatalf("durationMs = %d, want ~2000", body.DurationMs)
	}

	// The decode is expensive, so the result must survive in the cache.
	row, err := st.Q().GetTrackDuration(t.Context(), "t1")
	if err != nil {
		t.Fatalf("measurement was not cached: %v", err)
	}
	if row.DurationMs != body.DurationMs {
		t.Fatalf("cached %d, served %d", row.DurationMs, body.DurationMs)
	}
}

// A cached measurement is served without touching the file again.
func TestTrackDurationServesTheCacheWithoutTheFile(t *testing.T) {
	lib := &pagedLibrary{fakeLibrary: &fakeLibrary{}, ids: []string{"t1"}, paths: map[string]string{}}
	srv, st, cookie := durationServer(t, lib)
	if err := st.Q().UpsertTrackDuration(t.Context(), db.UpsertTrackDurationParams{
		TrackID: "t1", DurationMs: 180000, UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, cookie, http.MethodGet, "/api/v1/library/track/t1/duration", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		DurationMs int64 `json:"durationMs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.DurationMs != 180000 {
		t.Fatalf("durationMs = %d, want 180000", body.DurationMs)
	}
}

// A remote library has no file to decode, and the player then keeps the length
// the library reported.
func TestTrackDurationIsUnavailableWithoutALocalFile(t *testing.T) {
	lib := &pagedLibrary{fakeLibrary: &fakeLibrary{}, ids: []string{"t1"}, paths: map[string]string{}}
	srv, _, cookie := durationServer(t, lib)
	rec := do(t, srv, cookie, http.MethodGet, "/api/v1/library/track/t1/duration", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
}
