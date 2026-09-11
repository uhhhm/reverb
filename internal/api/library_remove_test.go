package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

type localPathLibrary struct {
	*fakeLibrary
	path string
	ok   bool
}

func (l *localPathLibrary) LocalTrackPath(string) (string, bool) { return l.path, l.ok }

func removeTrackTestServer(t *testing.T, musicDir string, lib *localPathLibrary, mode string) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "remove-track.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewService(st.Q(), time.Now)
	if err := authSvc.EnsureSeed(context.Background()); err != nil {
		t.Fatal(err)
	}
	return NewServer(Deps{
		AllowedHosts: testAllowedHosts,
		Auth:         authSvc,
		Library:      lib,
		Search:       registry.NewRegistry("search"),
		Downloader:   registry.NewRegistry("downloader"),
		MusicDir:     musicDir,
		LibraryStatus: func() (string, string) {
			return mode, "ready"
		},
	})
}

func TestRemoveLibraryTrackDeletesManagedFile(t *testing.T) {
	musicDir := t.TempDir()
	trackPath := filepath.Join(musicDir, "Artist", "Song.mp3")
	if err := os.MkdirAll(filepath.Dir(trackPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackPath, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := removeTrackTestServer(t, musicDir, &localPathLibrary{fakeLibrary: &fakeLibrary{}, path: trackPath, ok: true}, "built-in")

	rec := doAuthed(t, srv, http.MethodDelete, "/api/v1/library/track/t1", &http.Cookie{Name: sessionCookie})
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE track = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(trackPath); !os.IsNotExist(err) {
		t.Fatalf("track file still exists: %v", err)
	}
}

func TestRemoveLibraryTrackRejectsPathOutsideMusicDir(t *testing.T) {
	musicDir := t.TempDir()
	outsideDir := t.TempDir()
	trackPath := filepath.Join(outsideDir, "Song.mp3")
	if err := os.WriteFile(trackPath, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := removeTrackTestServer(t, musicDir, &localPathLibrary{fakeLibrary: &fakeLibrary{}, path: trackPath, ok: true}, "built-in")

	rec := doAuthed(t, srv, http.MethodDelete, "/api/v1/library/track/t1", &http.Cookie{Name: sessionCookie})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DELETE outside track = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(trackPath); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}
}

func TestRemoveLibraryTrackUnavailableForExternalLibrary(t *testing.T) {
	musicDir := t.TempDir()
	srv := removeTrackTestServer(t, musicDir, &localPathLibrary{fakeLibrary: &fakeLibrary{}}, "external")
	rec := doAuthed(t, srv, http.MethodDelete, "/api/v1/library/track/t1", &http.Cookie{Name: sessionCookie})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("DELETE external track = %d: %s", rec.Code, rec.Body.String())
	}
}
