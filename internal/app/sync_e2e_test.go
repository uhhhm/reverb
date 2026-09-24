package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/library/localfiles"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

// This file boots two complete Reverb runtimes -- the same composition root the
// desktop and server binaries use -- pairs them over libp2p on loopback, and
// drives both through their HTTP APIs the way the UI does. It is the guard for
// "everything replicates": playlists (including concurrent edits), listening
// history, Not interested marks, recommendation settings, and deletions, in
// both directions.

// syncDevice is one running Reverb instance under test.
type syncDevice struct {
	t        *testing.T
	name     string
	dbPath   string
	musicDir string
	profile  Profile
	// noDiscovery boots the device without mDNS or the DHT, the way it is
	// across a VPN.
	noDiscovery bool
	// freeSpace stands in for the phone's disk.
	freeSpace func(dir string) (int64, error)
	// builtIn gives a desktop the built-in library, whose music folder file
	// sync serves from; env points it at the fake backend instead of a
	// bundled Navidrome, which never starts.
	builtIn bool
	env     map[string]string
	rt      *Runtime
	srv     *httptest.Server
	stop    func()
}

// fakeSubsonic is an external library backend that owns nothing. Every
// endpoint answers ok, so the library adapter builds, the download manager and
// playlist service come up, and no Navidrome is spawned.
func fakeSubsonic(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch filepath.Base(r.URL.Path) {
		case "search3":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","searchResult3":{"song":[{"id":"desktop-only","title":"Desktop Only","artist":"Band","album":"Record","duration":180}]}}}`)
		case "stream":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = io.WriteString(w, "DESKTOP AUDIO")
		case "getPlaylists":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","playlists":{"playlist":[]}}}`)
		case "getArtists":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","artists":{"index":[]}}}`)
		case "getAlbumList2":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","albumList2":{"album":[]}}}`)
		case "getScanStatus", "startScan":
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","version":"1.16.1","scanStatus":{"scanning":false,"count":0}}}`)
		default:
			_, _ = io.WriteString(w, `{"subsonic-response":{"status":"ok","version":"1.16.1"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// deviceOption adjusts a device before it first boots.
type deviceOption func(*syncDevice)

func withoutDiscovery(d *syncDevice) { d.noDiscovery = true }

// withBuiltInLibrary boots a desktop whose library is its own music folder,
// which is what a desktop that holds files and replicates them is.
func withBuiltInLibrary(d *syncDevice) { d.builtIn = true }

func newSyncDevice(t *testing.T, name string, opts ...deviceOption) *syncDevice {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "reverb.db")
	lib := fakeSubsonic(t)

	d := &syncDevice{t: t, name: name, dbPath: dbPath, musicDir: filepath.Join(tmp, "music")}
	for _, o := range opts {
		o(d)
	}
	if d.builtIn {
		u, err := url.Parse(lib.URL)
		if err != nil {
			t.Fatal(err)
		}
		d.env = map[string]string{
			"REVERB_NAVIDROME_PORT": u.Port(),
			"REVERB_NAVIDROME_BIN":  filepath.Join(tmp, "no-navidrome"),
		}
	} else {
		// An enabled library row makes wiring pick external mode and build the
		// playlist service; the bundled spotDL downloader row is seeded by Build.
		st, err := store.Open(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(); err != nil {
			t.Fatal(err)
		}
		cfg, _ := json.Marshal(map[string]any{"url": lib.URL, "username": "u", "password": "p"})
		if err := st.Q().CreateAdapterInstance(context.Background(), db.CreateAdapterInstanceParams{
			ID: uuid.NewString(), Type: "library", Name: "subsonic", Enabled: 1, ConfigJson: string(cfg),
		}); err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
	}
	d.boot()
	t.Cleanup(func() { d.stop() })
	return d
}

// newPhoneDevice boots a phone-profile runtime: a folder library in its data
// directory, no Navidrome and no bundled tools.
func newPhoneDevice(t *testing.T, name string, opts ...deviceOption) *syncDevice {
	t.Helper()
	tmp := t.TempDir()
	d := &syncDevice{t: t, name: name, dbPath: filepath.Join(tmp, "reverb.db"), musicDir: filepath.Join(tmp, "music"), profile: ProfilePhone}
	for _, o := range opts {
		o(d)
	}
	d.boot()
	t.Cleanup(func() { d.stop() })
	return d
}

// boot builds and starts the runtime from the device's database. The phone
// profile derives its music folder from the data directory, so it is given no
// download directory.
func (d *syncDevice) boot() {
	d.t.Helper()
	env := map[string]string{}
	for k, v := range d.env {
		env[k] = v
	}
	if d.profile != ProfilePhone {
		env["REVERB_DOWNLOAD_DIR"] = d.musicDir
	}
	rt, err := Build(context.Background(), Options{
		DBPath:         d.dbPath,
		Version:        "test",
		Profile:        d.profile,
		P2PNoDiscovery: d.noDiscovery,
		FreeSpace:      d.freeSpace,
		Getenv:         func(k string) string { return env[k] },
	})
	if err != nil {
		d.t.Fatalf("%s: Build: %v", d.name, err)
	}
	if rt.Bundle.Sync == nil {
		d.t.Fatalf("%s: playlist service was not built", d.name)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt.StartBackground(ctx)
	srv := httptest.NewServer(api.NewServer(rt.Deps).Handler())
	stopped := false
	d.rt, d.srv, d.stop = rt, srv, func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		srv.Close()
		rt.Close()
	}
	if rt.P2P == nil || rt.P2PSyncer == nil {
		d.t.Fatalf("%s: p2p did not start", d.name)
	}
}

// call issues one API request and decodes the JSON reply into out (when non-nil).
func (d *syncDevice) call(method, path string, body any, out any) int {
	d.t.Helper()
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			d.t.Fatal(err)
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, d.srv.URL+"/api/v1"+path, payload)
	if err != nil {
		d.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.srv.Client().Do(req)
	if err != nil {
		d.t.Fatalf("%s %s %s: %v", d.name, method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			d.t.Fatalf("%s %s %s: decode %q: %v", d.name, method, path, raw, err)
		}
	}
	return resp.StatusCode
}

// must is call with the status asserted.
func (d *syncDevice) must(method, path string, body any, out any, want int) {
	d.t.Helper()
	if got := d.call(method, path, body, out); got != want {
		d.t.Fatalf("%s %s %s: status %d, want %d", d.name, method, path, got, want)
	}
}

// loopbackAddr is a full dial string for this device on 127.0.0.1, which is what
// a user would paste into the other device's pairing form on a network where
// discovery cannot find it.
func (d *syncDevice) loopbackAddr() string {
	d.t.Helper()
	for _, a := range d.rt.P2P.LibHost().Addrs() {
		s := a.String()
		if strings.HasPrefix(s, "/ip4/127.0.0.1/tcp/") {
			return s + "/p2p/" + d.rt.P2P.ID()
		}
	}
	d.t.Fatalf("%s: no loopback tcp listen address in %v", d.name, d.rt.P2P.Addrs())
	return ""
}

// pair walks the real pairing flow: a code minted on responder, redeemed by
// redeemer over libp2p through its own HTTP API, addressed the way a user on a
// VPN would: with the responder's full address.
func pair(t *testing.T, responder, redeemer *syncDevice) {
	t.Helper()
	pairWith(t, responder, redeemer, responder.loopbackAddr())
}

// pairByDiscovery is the LAN flow: the redeemer already has a connection to
// the responder (mDNS makes that happen on a real network) and the user has
// only the code, so the redeem carries no address at all.
func pairByDiscovery(t *testing.T, responder, redeemer *syncDevice) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := redeemer.rt.P2P.Connect(ctx, responder.loopbackAddr()); err != nil {
		t.Fatalf("%s could not connect to %s: %v", redeemer.name, responder.name, err)
	}
	pairWith(t, responder, redeemer, "")
}

func pairWith(t *testing.T, responder, redeemer *syncDevice, target string) {
	t.Helper()
	var code struct {
		Code string `json:"code"`
	}
	responder.must(http.MethodPost, "/pairing/code", nil, &code, http.StatusOK)
	var redeemed struct {
		DeviceID string `json:"deviceId"`
	}
	redeemer.must(http.MethodPost, "/p2p/pair/redeem", map[string]string{
		"peerId": target, "code": code.Code, "deviceName": redeemer.name,
	}, &redeemed, http.StatusOK)
	if redeemed.DeviceID == "" {
		t.Fatal("pairing returned no device id")
	}
	// The newly paired device is listed on the responder it paired with.
	var devices []struct {
		ID string `json:"id"`
	}
	responder.must(http.MethodGet, "/pairing/devices", nil, &devices, http.StatusOK)
	for _, d := range devices {
		if d.ID == redeemed.DeviceID {
			return
		}
	}
	t.Fatalf("%s paired as %s but is missing from %s's device list %v", redeemer.name, redeemed.DeviceID, responder.name, devices)
}

// restart stops the device and boots it again from the same database, the way
// a laptop comes back after a reboot: same identity, same paired devices, a
// fresh process with nothing in memory.
func (d *syncDevice) restart() {
	d.t.Helper()
	d.stop()
	d.boot()
}

// converge runs sync rounds on both devices until cond holds. Projection is
// asynchronous on the receiving side, so the condition is polled rather than
// checked once after a round.
func converge(t *testing.T, a, b *syncDevice, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		a.rt.P2PSyncer.SyncNow(context.Background())
		b.rt.P2PSyncer.SyncNow(context.Background())
		if cond() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("did not converge: %s\n%s round: %+v\n%s round: %+v", what,
		a.name, a.rt.P2PSyncer.Status(), b.name, b.rt.P2PSyncer.Status())
}

func (d *syncDevice) playlist(id string) (core.SyncedPlaylistDetail, bool) {
	var det core.SyncedPlaylistDetail
	if d.call(http.MethodGet, "/playlists/"+id, nil, &det) != http.StatusOK {
		return core.SyncedPlaylistDetail{}, false
	}
	return det, true
}

func (d *syncDevice) recentTitles() map[string]string {
	var rows []struct {
		ID    string
		Title string
	}
	d.call(http.MethodGet, "/stats/recent?limit=50", nil, &rows)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Title
	}
	return out
}

func (d *syncDevice) hasPlayTitled(title string) bool {
	for _, t := range d.recentTitles() {
		if t == title {
			return true
		}
	}
	return false
}

func (d *syncDevice) markKeys() map[string]bool {
	var resp struct {
		Marks []struct {
			Key string `json:"key"`
		} `json:"marks"`
	}
	d.call(http.MethodGet, "/not-interested", nil, &resp)
	out := map[string]bool{}
	for _, m := range resp.Marks {
		out[m.Key] = true
	}
	return out
}

func (d *syncDevice) settings() (adventurousness int, online bool) {
	var s struct {
		Adventurousness       int  `json:"adventurousness"`
		OnlineRecommendations bool `json:"onlineRecommendations"`
	}
	d.call(http.MethodGet, "/recommendations/settings", nil, &s)
	return s.Adventurousness, s.OnlineRecommendations
}

func trackBody(n int) map[string]any {
	return map[string]any{
		"source": "deezer", "externalId": fmt.Sprintf("dz-%d", n),
		"title": fmt.Sprintf("Track %d", n), "artist": "Band", "album": "Record",
		"durationMs": 180000, "download": false,
	}
}

func TestTwoDevicesConvergeOverP2P(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	a := newSyncDevice(t, "alpha")
	b := newSyncDevice(t, "beta")
	pair(t, a, b)

	// --- alpha edits, beta receives ---
	var created core.SyncedPlaylistDetail
	a.must(http.MethodPost, "/playlists", map[string]string{"name": "Road trip"}, &created, http.StatusCreated)
	pl := created.ID
	a.must(http.MethodPost, "/playlists/"+pl+"/tracks", trackBody(1), nil, http.StatusOK)
	a.must(http.MethodPut, "/playlists/"+pl, map[string]string{"name": "Road trip (renamed)"}, nil, http.StatusOK)
	a.must(http.MethodPost, "/plays", map[string]any{
		"title": "Alpha Song", "artist": "Band", "album": "Record", "durationMs": 180000, "msPlayed": 170000, "completed": true,
	}, nil, http.StatusNoContent)
	var mark struct {
		Key string `json:"key"`
	}
	a.must(http.MethodPost, "/not-interested", map[string]any{
		"kind": "track", "source": "deezer", "externalId": "dz-9", "title": "Track 9", "artist": "Band",
	}, &mark, http.StatusOK)
	a.must(http.MethodPut, "/recommendations/settings", map[string]any{"adventurousness": 77}, nil, http.StatusOK)

	converge(t, a, b, "playlist, play, mark and settings from alpha reach beta", func() bool {
		det, ok := b.playlist(pl)
		if !ok || det.Name != "Road trip (renamed)" || len(det.Tracks) != 1 {
			return false
		}
		adv, _ := b.settings()
		return b.hasPlayTitled("Alpha Song") && b.markKeys()[mark.Key] && adv == 77
	})
	if det, _ := b.playlist(pl); det.Mode != "once" || det.Source != "local" {
		t.Fatalf("beta's copy has identity %s/%s, want local/once", det.Source, det.Mode)
	}

	// --- beta edits, alpha receives ---
	b.must(http.MethodPost, "/playlists/"+pl+"/tracks", trackBody(2), nil, http.StatusOK)
	b.must(http.MethodPost, "/plays", map[string]any{
		"title": "Beta Song", "artist": "Band", "album": "Record", "durationMs": 200000, "msPlayed": 150000, "completed": false,
	}, nil, http.StatusNoContent)
	b.must(http.MethodPut, "/recommendations/settings", map[string]any{"onlineRecommendations": false}, nil, http.StatusOK)

	converge(t, a, b, "beta's track, play and settings reach alpha", func() bool {
		det, ok := a.playlist(pl)
		if !ok || len(det.Tracks) != 2 {
			return false
		}
		_, online := a.settings()
		return a.hasPlayTitled("Beta Song") && !online
	})

	// --- a third device paired only with beta learns everything, including
	// what alpha authored, over the relay; on the LAN it pairs with the code
	// alone ---
	c := newSyncDevice(t, "gamma")
	pairByDiscovery(t, b, c)
	converge(t, b, c, "gamma receives alpha's and beta's history through beta", func() bool {
		det, ok := c.playlist(pl)
		if !ok || det.Name != "Road trip (renamed)" || len(det.Tracks) != 2 {
			return false
		}
		adv, online := c.settings()
		return c.hasPlayTitled("Alpha Song") && c.hasPlayTitled("Beta Song") && c.markKeys()[mark.Key] && adv == 77 && !online
	})
	c.must(http.MethodPost, "/plays", map[string]any{
		"title": "Gamma Song", "artist": "Band", "album": "Record", "durationMs": 150000, "msPlayed": 150000, "completed": true,
	}, nil, http.StatusNoContent)
	converge(t, b, c, "gamma's play reaches beta", func() bool { return b.hasPlayTitled("Gamma Song") })
	converge(t, a, b, "gamma's play reaches alpha through beta", func() bool { return a.hasPlayTitled("Gamma Song") })

	// --- a restart keeps the pairing: same identity, same peers, no re-pair ---
	b.restart()
	a.must(http.MethodPost, "/plays", map[string]any{
		"title": "After Restart", "artist": "Band", "album": "Record", "durationMs": 100000, "msPlayed": 100000, "completed": true,
	}, nil, http.StatusNoContent)
	converge(t, a, b, "a play recorded after beta restarted still reaches it", func() bool { return b.hasPlayTitled("After Restart") })

	// --- concurrent edits on both sides before either syncs ---
	a.must(http.MethodPut, "/playlists/"+pl, map[string]string{"name": "Final name"}, nil, http.StatusOK)
	b.must(http.MethodPost, "/playlists/"+pl+"/tracks", trackBody(3), nil, http.StatusOK)
	converge(t, a, b, "a rename on alpha and an addition on beta both survive", func() bool {
		da, oka := a.playlist(pl)
		dbb, okb := b.playlist(pl)
		return oka && okb && da.Name == "Final name" && dbb.Name == "Final name" && len(da.Tracks) == 3 && len(dbb.Tracks) == 3
	})

	// --- removals ---
	b.must(http.MethodDelete, "/playlists/"+pl+"/tracks?source=deezer&externalId=dz-2", nil, nil, http.StatusOK)
	converge(t, a, b, "a track removed on beta disappears on alpha", func() bool {
		det, ok := a.playlist(pl)
		if !ok || len(det.Tracks) != 2 {
			return false
		}
		for _, tr := range det.Tracks {
			if tr.Key != nil && tr.Key.ExternalID == "dz-2" {
				return false
			}
		}
		return true
	})

	var alphaPlayID string
	for id, title := range a.recentTitles() {
		if title == "Alpha Song" {
			alphaPlayID = id
		}
	}
	if alphaPlayID == "" {
		t.Fatal("alpha's own play is missing from its history")
	}
	a.must(http.MethodDelete, "/plays/"+alphaPlayID, nil, nil, http.StatusNoContent)
	b.must(http.MethodDelete, "/not-interested", map[string]string{"key": mark.Key}, nil, http.StatusNoContent)
	a.must(http.MethodDelete, "/playlists/"+pl, nil, nil, http.StatusOK)

	converge(t, a, b, "deleted play, undone mark and deleted playlist propagate", func() bool {
		_, stillOnB := b.playlist(pl)
		_, stillOnA := a.playlist(pl)
		return !stillOnB && !stillOnA && !b.hasPlayTitled("Alpha Song") && !a.markKeys()[mark.Key]
	})
	converge(t, b, c, "the deletions reach gamma through beta", func() bool {
		_, stillOnC := c.playlist(pl)
		return !stillOnC && !c.hasPlayTitled("Alpha Song") && !c.markKeys()[mark.Key]
	})

	// Nothing that was deleted comes back once both sides are quiet.
	a.rt.P2PSyncer.SyncNow(context.Background())
	b.rt.P2PSyncer.SyncNow(context.Background())
	time.Sleep(500 * time.Millisecond)
	if _, back := b.playlist(pl); back {
		t.Fatal("deleted playlist reappeared on beta")
	}
	if b.hasPlayTitled("Alpha Song") {
		t.Fatal("deleted play reappeared on beta")
	}
	if !b.hasPlayTitled("Beta Song") || !a.hasPlayTitled("Beta Song") {
		t.Fatal("the surviving play was lost")
	}
}

// A phone is a reduced Device, not a remote: it pairs by typed code like any
// other device and the same history flows both ways through ordinary sync.
func TestPhoneConvergesWithDesktop(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop")
	phone := newPhoneDevice(t, "phone")
	pair(t, desktop, phone)

	// Library metadata is replicated even for a desktop track that has never
	// been played or placed in a playlist. Its audio remains on the desktop;
	// later Delegated-request work can use this identity to address it.
	desktopOnly := catalog.Identity{Kind: "track", Title: "Desktop Only", Artist: "Band", Album: "Record", DurationMs: 180000}
	converge(t, desktop, phone, "the desktop's library metadata reaches the phone", func() bool {
		_, found, err := phone.rt.catalog.Lookup(context.Background(), desktopOnly)
		return err == nil && found
	})

	// The phone's library is whatever is in its folder; file sync is what puts
	// files there, and a rescan is what it asks for once they land.
	if phone.rt.Deps.MusicDir != phone.musicDir {
		t.Fatalf("phone music folder = %q, want %q", phone.rt.Deps.MusicDir, phone.musicDir)
	}
	if err := os.MkdirAll(filepath.Join(phone.musicDir, "Band", "Record"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(phone.musicDir, "Band", "Record", "01 Held.mp3"), []byte("not really audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := phone.rt.Bundle.Library.StartScan(context.Background()); err != nil {
		t.Fatal(err)
	}
	var songs []core.Track
	phone.must(http.MethodGet, "/library/songs", nil, &songs, http.StatusOK)
	if len(songs) != 1 || songs[0].Title != "01 Held" || songs[0].Artist != "Band" {
		t.Fatalf("phone library = %+v", songs)
	}

	var created core.SyncedPlaylistDetail
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Commute"}, &created, http.StatusCreated)
	pl := created.ID
	desktop.must(http.MethodPost, "/playlists/"+pl+"/tracks", trackBody(1), nil, http.StatusOK)
	desktop.must(http.MethodPost, "/plays", map[string]any{
		"title": "Desk Song", "artist": "Band", "album": "Record", "durationMs": 180000, "msPlayed": 170000, "completed": true,
	}, nil, http.StatusNoContent)
	var deskMark struct {
		Key string `json:"key"`
	}
	desktop.must(http.MethodPost, "/not-interested", map[string]any{
		"kind": "track", "source": "deezer", "externalId": "dz-8", "title": "Track 8", "artist": "Band",
	}, &deskMark, http.StatusOK)
	desktop.must(http.MethodPut, "/recommendations/settings", map[string]any{"adventurousness": 30}, nil, http.StatusOK)

	converge(t, desktop, phone, "the desktop's playlist, play, mark and settings reach the phone", func() bool {
		det, ok := phone.playlist(pl)
		if !ok || det.Name != "Commute" || len(det.Tracks) != 1 {
			return false
		}
		adv, _ := phone.settings()
		return phone.hasPlayTitled("Desk Song") && phone.markKeys()[deskMark.Key] && adv == 30
	})

	// The two edits originate on different runtimes before either asks for a
	// sync round; neither may overwrite the other at convergence.
	desktop.must(http.MethodPut, "/playlists/"+pl, map[string]string{"name": "Commute together"}, nil, http.StatusOK)
	phone.must(http.MethodPost, "/playlists/"+pl+"/tracks", trackBody(2), nil, http.StatusOK)
	phone.must(http.MethodPost, "/plays", map[string]any{
		"title": "Pocket Song", "artist": "Band", "album": "Record", "durationMs": 200000, "msPlayed": 190000, "completed": true,
	}, nil, http.StatusNoContent)
	var phoneMark struct {
		Key string `json:"key"`
	}
	phone.must(http.MethodPost, "/not-interested", map[string]any{
		"kind": "artist", "source": "deezer", "id": "41", "name": "Somebody Else",
	}, &phoneMark, http.StatusOK)
	phone.must(http.MethodPut, "/recommendations/settings", map[string]any{"onlineRecommendations": false}, nil, http.StatusOK)

	converge(t, desktop, phone, "the phone's track, play, mark and settings reach the desktop", func() bool {
		det, ok := desktop.playlist(pl)
		if !ok || det.Name != "Commute together" || len(det.Tracks) != 2 {
			return false
		}
		_, online := desktop.settings()
		return desktop.hasPlayTitled("Pocket Song") && desktop.markKeys()[phoneMark.Key] && !online
	})

	// A restarted phone keeps its pairing and its folder library.
	phone.restart()
	if phone.rt.Bundle.Library.Name() != localfiles.Name {
		t.Fatalf("phone came back with library %s", phone.rt.Bundle.Library.Name())
	}
	desktop.must(http.MethodPost, "/plays", map[string]any{
		"title": "After Phone Restart", "artist": "Band", "album": "Record", "durationMs": 100000, "msPlayed": 100000, "completed": true,
	}, nil, http.StatusNoContent)
	converge(t, desktop, phone, "a play reaches the restarted phone", func() bool { return phone.hasPlayTitled("After Phone Restart") })
}

func TestPhoneCopiesSpotifyCredentialsAndKeepsSourceWithoutDesktop(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop")
	phone := newPhoneDevice(t, "phone")
	if err := desktop.rt.Store.Q().CreateAdapterInstance(context.Background(), db.CreateAdapterInstanceParams{
		ID: uuid.NewString(), Type: "search", Name: "spotify", Enabled: 1,
		ConfigJson: `{"client_id":"desktop-client","client_secret":"desktop-secret"}`,
	}); err != nil {
		t.Fatal(err)
	}
	pair(t, desktop, phone)
	credentials, err := phone.rt.CopySpotifyCredentials(context.Background())
	if err != nil || credentials.ClientID != "desktop-client" || credentials.ClientSecret != "desktop-secret" {
		t.Fatal("paired phone did not copy desktop search credentials")
	}
	phone.env = map[string]string{
		"REVERB_SPOTIFY_CLIENT_ID":     credentials.ClientID,
		"REVERB_SPOTIFY_CLIENT_SECRET": credentials.ClientSecret,
	}
	phone.restart()
	desktop.stop()
	sources := phone.rt.Bundle.Aggregator.Sources()
	if len(sources) != 2 || sources[0].Name() != "deezer" || sources[1].Name() != "spotify" {
		t.Fatalf("phone sources without desktop = %+v", sources)
	}
}

func TestPhoneDelegatesNonOfflineTrackAndMarksItUnavailableWhenPeerStops(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop")
	phone := newPhoneDevice(t, "phone")
	pair(t, desktop, phone)

	var created core.SyncedPlaylistDetail
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Stream me"}, &created, http.StatusCreated)
	desktop.must(http.MethodPost, "/playlists/"+created.ID+"/tracks", map[string]any{
		"source": "library", "externalId": "desktop-only", "title": "Desktop Only", "artist": "Band", "album": "Record",
		"durationMs": 180000, "download": false,
	}, nil, http.StatusOK)

	var phoneDetail core.SyncedPlaylistDetail
	converge(t, desktop, phone, "the phone receives a delegatable library track", func() bool {
		if phone.call(http.MethodGet, "/playlists/"+created.ID, nil, &phoneDetail) != http.StatusOK || len(phoneDetail.Tracks) != 1 {
			return false
		}
		return phoneDetail.Tracks[0].CanonicalID != "" && phoneDetail.Tracks[0].Playback == core.PlaybackDelegated
	})

	track := phoneDetail.Tracks[0]
	var browse []core.CatalogLibraryTrack
	phone.must(http.MethodGet, "/library/catalog/tracks?q=Desktop", nil, &browse, http.StatusOK)
	if len(browse) != 1 || browse[0].ID != track.CanonicalID || browse[0].Playback != core.PlaybackDelegated {
		t.Fatalf("phone household library browse = %+v", browse)
	}
	resp, err := phone.srv.Client().Get(phone.srv.URL + "/api/v1/stream/" + track.CanonicalID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "DESKTOP AUDIO" {
		t.Fatalf("delegated stream: status=%d body=%q", resp.StatusCode, body)
	}
	// Metadata edits on the desktop travel under the stable catalog identity,
	// even though this phone has no local libraryTrack for the delegated row.
	desktop.must(http.MethodPut, "/library/track/desktop-only/name", map[string]string{
		"title": "Renamed on Desktop", "artist": "New Artist",
	}, nil, http.StatusOK)
	desktop.must(http.MethodPut, "/library/track/desktop-only/crop", map[string]int{
		"startMs": 3000, "endMs": 9000,
	}, nil, http.StatusOK)
	converge(t, desktop, phone, "desktop track edits appear on the phone", func() bool {
		var detail core.SyncedPlaylistDetail
		if phone.call(http.MethodGet, "/playlists/"+created.ID, nil, &detail) != http.StatusOK || len(detail.Tracks) != 1 {
			return false
		}
		row := detail.Tracks[0]
		return row.Title == "Renamed on Desktop" && row.Artist == "New Artist" && row.CropStartMs == 3000 && row.CropEndMs == 9000
	})

	desktop.stop()
	phoneDetail = core.SyncedPlaylistDetail{}
	phone.must(http.MethodGet, "/playlists/"+created.ID, nil, &phoneDetail, http.StatusOK)
	if got := phoneDetail.Tracks[0].Playback; got != core.PlaybackUnavailable {
		t.Fatalf("playback after desktop stopped = %q, want unavailable", got)
	}
	browse = nil
	phone.must(http.MethodGet, "/library/catalog/tracks?q=Desktop", nil, &browse, http.StatusOK)
	if len(browse) != 1 || browse[0].Playback != core.PlaybackUnavailable {
		t.Fatalf("offline household library browse = %+v", browse)
	}
}

// Scanning the desktop's pairing QR code pairs a phone with no discovery at
// all: the payload says where the desktop is, and the code is still proved
// rather than sent.
func TestPhonePairsFromQRPayloadWithoutDiscovery(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop", withoutDiscovery)
	phone := newPhoneDevice(t, "phone", withoutDiscovery)

	var code struct {
		Code      string `json:"code"`
		QRPayload string `json:"qrPayload"`
		QRSvg     string `json:"qrSvg"`
	}
	desktop.must(http.MethodPost, "/pairing/code", nil, &code, http.StatusOK)
	if code.QRPayload == "" {
		t.Skip("the desktop has no non-loopback address to put in a QR code")
	}
	if code.QRSvg == "" {
		t.Fatal("the pairing screen has no QR code to show")
	}
	if n := len(phone.rt.P2P.LibHost().Network().ConnsToPeer(desktop.rt.P2P.LibHost().ID())); n != 0 {
		t.Fatalf("the phone already had %d connection(s) to the desktop; nothing would prove the payload was used", n)
	}

	var redeemed struct {
		DeviceID string `json:"deviceId"`
	}
	phone.must(http.MethodPost, "/p2p/pair/redeem-qr", map[string]string{
		"payload": code.QRPayload, "deviceName": "phone",
	}, &redeemed, http.StatusOK)
	var devices []struct {
		ID string `json:"id"`
	}
	desktop.must(http.MethodGet, "/pairing/devices", nil, &devices, http.StatusOK)
	found := false
	for _, d := range devices {
		found = found || d.ID == redeemed.DeviceID
	}
	if !found {
		t.Fatalf("the phone paired as %s but the desktop lists %v", redeemed.DeviceID, devices)
	}

	// The code is single-use: scanning it again is refused.
	var again struct {
		Error string `json:"error"`
	}
	if got := phone.call(http.MethodPost, "/p2p/pair/redeem-qr", map[string]string{
		"payload": code.QRPayload, "deviceName": "phone",
	}, &again); got == http.StatusOK {
		t.Fatal("a used pairing code paired a second time")
	}

	desktop.must(http.MethodPost, "/plays", map[string]any{
		"title": "Scanned Song", "artist": "Band", "album": "Record", "durationMs": 100000, "msPlayed": 100000, "completed": true,
	}, nil, http.StatusNoContent)
	converge(t, desktop, phone, "a play reaches the phone paired by QR", func() bool { return phone.hasPlayTitled("Scanned Song") })
}
