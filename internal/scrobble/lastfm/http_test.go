package lastfm

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/recommend"
)

// An endpoint that never stops writing must not be buffered whole.
func TestReadsOfAnOversizedResponseFailRatherThanBuffer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"similartracks":{"track":[`)); err != nil {
			return
		}
		chunk := bytes.Repeat([]byte(" "), 1<<16)
		for written := 0; written <= maxResponseBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	src := NewSimilarity(newTestAdapter(srv.URL), func() string { return "key1" })
	_, err := src.SimilarTracks(context.Background(), recommend.TrackSeed{Artist: "A", Title: "B"}, 30)
	if err == nil {
		t.Fatal("an oversized response must fail the read")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("error = %v, want it to name the size limit", err)
	}
}

// A body at the limit still reads, so normal responses are unaffected.
func TestReadBodyKeepsAResponseUpToTheLimit(t *testing.T) {
	body := bytes.Repeat([]byte("x"), maxResponseBytes)
	got, err := readBody(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("a body at the limit must read: %v", err)
	}
	if len(got) != maxResponseBytes {
		t.Fatalf("read %d bytes, want %d", len(got), maxResponseBytes)
	}
	if _, err := readBody(bytes.NewReader(append(body, 'x'))); err == nil {
		t.Fatal("one byte over the limit must fail")
	}
}

// A Last.fm endpoint that accepts the connection and then never answers must
// not hold the scrobble worker, which submits on the long-lived app context.
func TestRequestsStopWaitingOnAStalledEndpoint(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })

	a := New()
	if a.client.Timeout <= 0 {
		t.Fatal("the Last.fm client must carry its own timeout")
	}
	a.baseURL, a.client.Timeout = strings.TrimRight(srv.URL, "/")+"/", 50*time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := NewSimilarity(a, func() string { return "key1" }).
			SimilarTracks(context.Background(), recommend.TrackSeed{Artist: "A", Title: "B"}, 30)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled endpoint must fail the request, not succeed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request hung on a stalled endpoint")
	}
}
