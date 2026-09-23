// Command reverb-testpeer is the device the iOS smoke test pairs with. It runs
// a full Reverb runtime holding one playlist of one short track, and adds two
// endpoints: /testpeer/pairing mints a pairing code and says where to dial it,
// so a UI test can type both into the app, and /testpeer/stop shuts the peer
// down, so the test can show the phone plays on its own. Run it on the Mac, where the
// simulator reaches it on 127.0.0.1.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/app"
	"github.com/uhhhm/reverb/internal/p2p"
)

const (
	playlistName = "Smoke Test"
	trackTitle   = "Smoke Tone"
	trackArtist  = "Smoke"
	trackAlbum   = "Tests"
	// Long enough for the UI test to see progress, pause, seek and resume
	// before it ends.
	toneSeconds = 20
)

func main() {
	addr := flag.String("addr", "127.0.0.1:47300", "HTTP listen address")
	p2pPort := flag.Int("p2p-port", 47301, "libp2p listen port")
	dir := flag.String("dir", "", "data directory (default: a new temporary one)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *addr, *p2pPort, *dir, func(addr, dir string) {
		log.Printf("reverb-testpeer: API on http://%s, data in %s", addr, dir)
	}); err != nil {
		log.Fatal(err)
	}
}

// run serves the test peer until ctx ends, calling ready once it listens.
func run(ctx context.Context, addr string, p2pPort int, dir string, ready func(addr, dir string)) error {
	// The extra endpoint mints pairing codes with none of the API's guards,
	// so it is only ever served on loopback.
	if host, _, err := net.SplitHostPort(addr); err != nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("-addr %q: the test peer only listens on a loopback address", addr)
	}
	if dir == "" {
		d, err := os.MkdirTemp("", "reverb-testpeer-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(d)
		dir = d
	}
	// The phone profile keeps its library in <dir>/music and needs no
	// Navidrome, which is all a stand-in for the desktop has to provide here:
	// a device holding files it advertises through file sync.
	music := filepath.Join(dir, "music", trackArtist, trackAlbum)
	if err := os.MkdirAll(music, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(music, trackTitle+".wav"), tone(toneSeconds), 0o644); err != nil {
		return err
	}
	rt, err := app.Build(context.Background(), app.Options{
		DBPath:  filepath.Join(dir, "reverb.db"),
		Version: "testpeer",
		P2PPort: p2pPort,
		Profile: app.ProfilePhone,
		Getenv:  func(string) string { return "" },
	})
	if err != nil {
		return err
	}
	defer rt.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rt.StartBackground(ctx)

	apiHandler := api.NewServer(rt.Deps).Handler()
	if err := seedPlaylist(apiHandler); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /testpeer/pairing", func(w http.ResponseWriter, _ *http.Request) {
		var code struct {
			Code      string `json:"code"`
			ExpiresAt int64  `json:"expiresAt"`
		}
		if err := call(apiHandler, http.MethodPost, "/pairing/code", nil, &code); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		dial, err := loopbackAddr(rt)
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		// The same code as a pairing link, dialled on loopback, for the test
		// that opens one the way the system Camera would.
		link, err := p2p.EncodePairPayload(p2p.PairPayload{Code: code.Code, ExpiresAt: code.ExpiresAt, Addrs: []string{dial}})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"code": code.Code, "address": dial, "link": link, "playlist": playlistName, "track": trackTitle,
		})
	})
	// Stopping the peer is how a test shows the phone plays with no device
	// to reach.
	mux.HandleFunc("POST /testpeer/stop", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		cancel()
	})
	mux.Handle("/", apiHandler)
	// The two test routes skip the API's guards; a request a web page sent
	// carries an Origin, and none of them are the test's.
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/testpeer/") && r.Header.Get("Origin") != "" {
			http.Error(w, "cross-origin request blocked", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: guarded}
	shutDown := make(chan struct{})
	defer func() {
		cancel()
		<-shutDown
	}()
	go func() {
		defer close(shutDown)
		<-ctx.Done()
		// Graceful, so the answer to /testpeer/stop still reaches the caller.
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = srv.Shutdown(shutdown)
	}()
	ready(ln.Addr().String(), dir)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// seedPlaylist makes the one playlist the smoke test keeps offline.
func seedPlaylist(h http.Handler) error {
	var created struct {
		ID string `json:"id"`
	}
	if err := call(h, http.MethodPost, "/playlists", map[string]string{"name": playlistName}, &created); err != nil {
		return err
	}
	return call(h, http.MethodPost, "/playlists/"+created.ID+"/tracks", map[string]any{
		"source": "testpeer", "externalId": "smoke-tone", "title": trackTitle, "artist": trackArtist,
		"album": trackAlbum, "durationMs": toneSeconds * 1000, "download": false,
	}, nil)
}

func call(h http.Handler, method, path string, body, out any) error {
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return err
		}
	}
	req := httptest.NewRequest(method, "/api/v1"+path, &payload)
	req.Host = "127.0.0.1"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, rec.Code, rec.Body.String())
	}
	if out != nil {
		return json.Unmarshal(rec.Body.Bytes(), out)
	}
	return nil
}

// loopbackAddr is this runtime's libp2p address on 127.0.0.1, which the
// simulator shares with the Mac.
func loopbackAddr(rt *app.Runtime) (string, error) {
	if rt.P2P == nil {
		return "", fmt.Errorf("p2p is not running")
	}
	for _, a := range rt.P2P.LibHost().Addrs() {
		if s := a.String(); strings.HasPrefix(s, "/ip4/127.0.0.1/tcp/") {
			return s + "/p2p/" + rt.P2P.ID(), nil
		}
	}
	return "", fmt.Errorf("no loopback tcp address among %v", rt.P2P.Addrs())
}

// tone is a WAV file of seconds of a 440 Hz sine, something AVPlayer plays.
func tone(seconds int) []byte {
	const rate = 44100
	samples := rate * seconds
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + samples*2))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1)) // PCM
	w(uint16(1)) // mono
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(samples * 2))
	for i := 0; i < samples; i++ {
		w(int16(math.Sin(2*math.Pi*440*float64(i)/rate) * 8000))
	}
	return b.Bytes()
}
