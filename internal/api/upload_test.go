package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

func uploadServer(t *testing.T, musicDir, mode string) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/upload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:          authSvc,
		Search:        registry.NewRegistry("search"),
		Downloader:    registry.NewRegistry("downloader"),
		MusicDir:      musicDir,
		LibraryStatus: func() (string, string) { return mode, "ready" },
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

func doUpload(t *testing.T, srv *Server, cookie *http.Cookie, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, data := range files {
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/library/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestUploadWritesIntoTheMusicDirectory(t *testing.T) {
	dir := t.TempDir()
	srv, cookie := uploadServer(t, dir, "built-in")
	rec := doUpload(t, srv, cookie, map[string][]byte{"Song.mp3": []byte("audio-bytes")})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, uploadDirName, "Song.mp3"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if string(got) != "audio-bytes" {
		t.Fatalf("contents = %q", got)
	}
}

func TestUploadRejectsUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	srv, cookie := uploadServer(t, dir, "built-in")
	rec := doUpload(t, srv, cookie, map[string][]byte{"notes.txt": []byte("hi")})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, uploadDirName, "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("a rejected file must not be written")
	}
}

// In external mode the music directory belongs to somebody else's Navidrome, so
// there is nowhere Reverb may write.
func TestUploadUnavailableInExternalMode(t *testing.T) {
	srv, cookie := uploadServer(t, t.TempDir(), "external")
	if rec := doUpload(t, srv, cookie, map[string][]byte{"Song.mp3": []byte("x")}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// A filename is attacker-controlled input: it must never steer the write out of
// the upload directory.
func TestUploadFilenameCannotEscapeTheDirectory(t *testing.T) {
	dir := t.TempDir()
	srv, cookie := uploadServer(t, dir, "built-in")
	rec := doUpload(t, srv, cookie, map[string][]byte{"../../evil.mp3": []byte("x")})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.mp3")); !os.IsNotExist(err) {
		t.Fatal("upload escaped into the music dir root")
	}
	if _, err := os.Stat(filepath.Join(dir, uploadDirName, "evil.mp3")); err != nil {
		t.Fatalf("upload should have landed as a plain name: %v", err)
	}
}

// An upload must never silently replace a track that is already there.
func TestUploadDoesNotOverwriteExistingFile(t *testing.T) {
	dir := t.TempDir()
	srv, cookie := uploadServer(t, dir, "built-in")
	if rec := doUpload(t, srv, cookie, map[string][]byte{"Song.mp3": []byte("first")}); rec.Code != http.StatusOK {
		t.Fatalf("first upload: %s", rec.Body.String())
	}
	if rec := doUpload(t, srv, cookie, map[string][]byte{"Song.mp3": []byte("second")}); rec.Code != http.StatusOK {
		t.Fatalf("second upload: %s", rec.Body.String())
	}
	first, _ := os.ReadFile(filepath.Join(dir, uploadDirName, "Song.mp3"))
	if string(first) != "first" {
		t.Fatalf("original was overwritten: %q", first)
	}
	if _, err := os.Stat(filepath.Join(dir, uploadDirName, "Song (2).mp3")); err != nil {
		t.Fatalf("second copy missing: %v", err)
	}
}

func TestUploadReportsWhatItAccepted(t *testing.T) {
	dir := t.TempDir()
	srv, cookie := uploadServer(t, dir, "built-in")
	rec := doUpload(t, srv, cookie, map[string][]byte{"Good.flac": []byte("x"), "bad.exe": []byte("y")})
	var resp uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Uploaded) != 1 || resp.Uploaded[0].Name != "Good.flac" {
		t.Fatalf("uploaded = %+v", resp.Uploaded)
	}
	if resp.Rejected["bad.exe"] == "" {
		t.Fatalf("rejected = %+v", resp.Rejected)
	}
}
