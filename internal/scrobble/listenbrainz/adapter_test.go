package listenbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/scrobble"
)

const secretToken = "secret-token-1234"

func TestScrobbleSubmitsImportWithTokenHeader(t *testing.T) {
	var body map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/submit-listens" || r.Method != http.MethodPost {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	a := newTestAdapter(srv.URL, srv.Client())
	plays := []scrobble.ScrobblePlay{
		{Track: scrobble.Track{Title: "One", Artist: "Egzod; Maestro Chives", Album: "LP", DurationMs: 180000}, PlayedAt: 100},
		{Track: scrobble.Track{Title: "Two", Artist: "B"}, PlayedAt: 200},
	}
	n, err := a.Scrobble(context.Background(), scrobble.Creds{SessionKey: secretToken}, plays)
	if err != nil || n != 2 {
		t.Fatalf("Scrobble = %d, %v", n, err)
	}
	if auth != "Token "+secretToken {
		t.Fatalf("Authorization = %q", auth)
	}
	if body["listen_type"] != "import" {
		t.Fatalf("listen_type = %v", body["listen_type"])
	}
	payload := body["payload"].([]any)
	first := payload[0].(map[string]any)
	meta := first["track_metadata"].(map[string]any)
	if first["listened_at"] != float64(100) || meta["artist_name"] != "Egzod; Maestro Chives" || meta["track_name"] != "One" || meta["release_name"] != "LP" {
		t.Fatalf("first listen = %v", first)
	}
	info := meta["additional_info"].(map[string]any)
	if info["duration_ms"] != float64(180000) || info["submission_client"] != "Reverb" {
		t.Fatalf("additional_info = %v", info)
	}
}

func TestSingleListenAndNowPlayingTypes(t *testing.T) {
	var types []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ListenType string           `json:"listen_type"`
			Payload    []map[string]any `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		types = append(types, body.ListenType)
		if body.ListenType == "playing_now" {
			if _, ok := body.Payload[0]["listened_at"]; ok {
				t.Errorf("playing_now carries listened_at")
			}
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	a := newTestAdapter(srv.URL, srv.Client())
	c := scrobble.Creds{SessionKey: secretToken}

	if _, err := a.Scrobble(context.Background(), c, []scrobble.ScrobblePlay{{Track: scrobble.Track{Title: "T", Artist: "A"}, PlayedAt: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := a.NowPlaying(context.Background(), c, scrobble.Track{Title: "T", Artist: "A"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(types, ",") != "single,playing_now" {
		t.Fatalf("listen types = %v", types)
	}
}

func TestUnauthorizedIsErrAuthAndNeverCarriesTheToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// A server echoing the header must not get it into an error either.
		_, _ = w.Write([]byte(`{"code":401,"error":"Invalid authorization token ` + secretToken + `"}`))
	}))
	defer srv.Close()
	a := newTestAdapter(srv.URL, srv.Client())

	_, err := a.Scrobble(context.Background(), scrobble.Creds{SessionKey: secretToken}, []scrobble.ScrobblePlay{{Track: scrobble.Track{Title: "T", Artist: "A"}, PlayedAt: 1}})
	if !errors.Is(err, scrobble.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if strings.Contains(err.Error(), secretToken) {
		t.Fatalf("error leaks the token: %v", err)
	}
	if _, err := a.ValidateToken(context.Background(), secretToken); !errors.Is(err, scrobble.ErrAuth) || strings.Contains(err.Error(), secretToken) {
		t.Fatalf("validate err = %v", err)
	}
}

func TestServerErrorIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	a := newTestAdapter(srv.URL, srv.Client())

	_, err := a.Scrobble(context.Background(), scrobble.Creds{SessionKey: secretToken}, []scrobble.ScrobblePlay{{Track: scrobble.Track{Title: "T", Artist: "A"}, PlayedAt: 1}})
	if err == nil || errors.Is(err, scrobble.ErrAuth) {
		t.Fatalf("err = %v, want a transient error", err)
	}
}

func TestValidateToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/1/validate-token" || r.Header.Get("Authorization") != "Token "+secretToken {
			_, _ = w.Write([]byte(`{"code":200,"message":"Token invalid.","valid":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":200,"message":"Token valid.","valid":true,"user_name":"lbuser"}`))
	}))
	defer srv.Close()
	a := newTestAdapter(srv.URL, srv.Client())

	name, err := a.ValidateToken(context.Background(), secretToken)
	if err != nil || name != "lbuser" {
		t.Fatalf("ValidateToken = %q, %v", name, err)
	}
	if _, err := a.ValidateToken(context.Background(), "other"); !errors.Is(err, scrobble.ErrAuth) {
		t.Fatalf("invalid token err = %v, want ErrAuth", err)
	}
}

// An upload runs on the scrobble worker's long-lived context, so a ListenBrainz
// endpoint that accepts the connection and then never answers would otherwise
// stall the whole queue.
func TestUploadStopsWaitingOnAStalledEndpoint(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); srv.Close() })

	a := New()
	if a.client.Timeout <= 0 {
		t.Fatal("the upload client must carry its own timeout")
	}
	a.baseURL, a.client.Timeout = srv.URL, 50*time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := a.Scrobble(context.Background(), scrobble.Creds{SessionKey: secretToken},
			[]scrobble.ScrobblePlay{{Track: scrobble.Track{Title: "t", Artist: "a"}, PlayedAt: 1}})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled endpoint must fail the upload, not succeed")
		}
		if strings.Contains(err.Error(), secretToken) {
			t.Fatalf("the token must not appear in an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the upload hung on a stalled endpoint")
	}
}
