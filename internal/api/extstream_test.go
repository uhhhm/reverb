package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

type fakeExtStream struct {
	url         string
	err         error
	mu          sync.Mutex
	resolves    int
	invalidated int
	lastArtist  string
	lastTitle   string
}

func (f *fakeExtStream) ResolveHinted(_ context.Context, _, _, artist, title string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves++
	f.lastArtist, f.lastTitle = artist, title
	return f.url, f.err
}

func (f *fakeExtStream) Invalidate(_, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidated++
}

func (f *fakeExtStream) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resolves, f.invalidated
}

func extStreamServer(t *testing.T, ext ExternalStreamResolver) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/ext.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{
		Auth:           authSvc,
		Search:         registry.NewRegistry("search"),
		Downloader:     registry.NewRegistry("downloader"),
		ExternalStream: ext,
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

// The point of the endpoint: audio reaches the listener without a download job,
// a file, or a library scan. It must also be progressive and seekable, which
// means forwarding Range upstream and copying the 206 back verbatim.
func TestExternalStreamProxiesAudioWithRange(t *testing.T) {
	var gotRange string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "audio/webm")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("AUDIO"))
	}))
	defer upstream.Close()

	srv, cookie := extStreamServer(t, &fakeExtStream{url: upstream.URL})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/external/stream/deezer/123", nil)
	req.Header.Set("Range", "bytes=5-9")
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206: %s", rec.Code, rec.Body.String())
	}
	if gotRange != "bytes=5-9" {
		t.Errorf("upstream Range = %q, want the browser's", gotRange)
	}
	if rec.Body.String() != "AUDIO" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 5-9/10" {
		t.Errorf("Content-Range = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/webm" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store for a short-lived URL", got)
	}
}

// A resolved URL can expire before its cache TTL. The upstream says 403; the
// listener should get audio, not a dead stream.
func TestExternalStreamReresolvesOnExpiredURL(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte("AUDIO"))
	}))
	defer upstream.Close()

	ext := &fakeExtStream{url: upstream.URL}
	srv, cookie := extStreamServer(t, ext)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/external/stream/deezer/123", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "AUDIO" {
		t.Fatalf("status %d body %q, want a successful retry", rec.Code, rec.Body.String())
	}
	resolves, invalidated := ext.counts()
	if invalidated != 1 {
		t.Errorf("invalidated %d times, want 1", invalidated)
	}
	if resolves != 2 {
		t.Errorf("resolved %d times, want 2", resolves)
	}
}

func TestExternalStreamUnavailableWithoutResolver(t *testing.T) {
	srv, cookie := extStreamServer(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/external/stream/deezer/123", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestExternalStreamReportsResolveFailure(t *testing.T) {
	srv, cookie := extStreamServer(t, &fakeExtStream{err: errors.New("no playable source found")})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/external/stream/deezer/123", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no playable source") {
		t.Errorf("body = %q, want the resolve failure surfaced", rec.Body.String())
	}
}

// A track that isn't in the library has no file and is unknown to the backend,
// so the lyrics handler's optional local-file lookup must be skipped for it —
// otherwise every play logs a "Song not found" error against the library.
func TestIsExternalTrackID(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"deezer:1144909952", true},
		{"spotify:4h47YiL87c9mmfBGwMTvai", true},
		{"al2f3c9d8e7b6a5", false},
		{"trk_abc123", false},
		{"", false},
	} {
		if got := isExternalTrackID(tc.id); got != tc.want {
			t.Errorf("isExternalTrackID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// The upstream throttles a range-less GET to roughly playback speed — measured
// at ~7 KB/s against ~4 MB/s for the same URL requested as "bytes=0-". A
// browser's first request carries no Range, so the proxy must supply one; the
// resulting 206 is then reported to the browser as the 200 it asked for.
func TestExternalStreamRequestsRangeWhenBrowserSendsNone(t *testing.T) {
	var gotRange string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "audio/webm")
		w.Header().Set("Content-Range", "bytes 0-4/5")
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("AUDIO"))
	}))
	defer upstream.Close()

	srv, cookie := extStreamServer(t, &fakeExtStream{url: upstream.URL})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/external/stream/deezer/123", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)

	if gotRange != "bytes=0-" {
		t.Errorf("upstream Range = %q, want an open-ended range to dodge throttling", gotRange)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a client that sent no Range", rec.Code)
	}
	if got := rec.Header().Get("Content-Range"); got != "" {
		t.Errorf("Content-Range = %q, want none on a 200", got)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes so the player offers seeking", got)
	}
	if rec.Body.String() != "AUDIO" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// A genuine partial must never be downgraded to 200: only a response covering
// the whole resource is the substituted range's own doing.
func TestExternalStreamKeepsRealPartialFromMidFileUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("AUDIO"))
	}))
	defer upstream.Close()

	srv, cookie := extStreamServer(t, &fakeExtStream{url: upstream.URL})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/external/stream/deezer/123", nil)
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Errorf("status = %d, want the upstream's 206 preserved", rec.Code)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 5-9/10" {
		t.Errorf("Content-Range = %q, want it preserved", got)
	}
}
