package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/offlineset"
)

// offlineRadioTrack is one file of the scenario: where it sits on the desktop
// and what its tags say.
type offlineRadioTrack struct {
	rel, title, artist, album string
}

// addTaggedFile is addFile for any artist and album.
func (d *syncDevice) addTaggedFile(tr offlineRadioTrack, size int) []byte {
	d.t.Helper()
	data := taggedMP3(tr.title, tr.artist, tr.album, size)
	p := filepath.Join(d.musicDir, filepath.FromSlash(tr.rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		d.t.Fatal(err)
	}
	return data
}

func trackSong(tr offlineRadioTrack) map[string]any {
	return map[string]any{
		"source": "deezer", "externalId": "dz-" + tr.artist + "-" + tr.title, "title": tr.title,
		"artist": tr.artist, "album": tr.album, "durationMs": 180000, "download": false,
	}
}

// A phone with no internet and online recommendations off still has Radio:
// local similarity scores the tracks the phone can play itself, its offline
// set, even though they are copies of the desktop's files and the phone
// holds no library bindings for them (ADR 0003).
func TestPhoneRadioPlaysFromItsOfflineSet(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop", withBuiltInLibrary)
	phone := newPhoneDevice(t, "phone")
	pair(t, desktop, phone)

	// Fourteen tracks by five artists make up the phone's offline playlist.
	// The seed's artist has one more track in it, on the same album.
	roadTrip := []offlineRadioTrack{
		{"Aurora Lane/Tides/01 Signal.mp3", "Signal", "Aurora Lane", "Tides"},
		{"Aurora Lane/Tides/02 Harbor.mp3", "Harbor", "Aurora Lane", "Tides"},
		{"Kite Theory/Updraft/01 Glide.mp3", "Glide", "Kite Theory", "Updraft"},
		{"Kite Theory/Updraft/02 Thermal.mp3", "Thermal", "Kite Theory", "Updraft"},
		{"Kite Theory/Updraft/03 Crosswind.mp3", "Crosswind", "Kite Theory", "Updraft"},
		{"Moss Parade/Lichen/01 Damp.mp3", "Damp", "Moss Parade", "Lichen"},
		{"Moss Parade/Lichen/02 Spore.mp3", "Spore", "Moss Parade", "Lichen"},
		{"Moss Parade/Lichen/03 Canopy.mp3", "Canopy", "Moss Parade", "Lichen"},
		{"Night Ferry/Crossing/01 Gangway.mp3", "Gangway", "Night Ferry", "Crossing"},
		{"Night Ferry/Crossing/02 Bow.mp3", "Bow", "Night Ferry", "Crossing"},
		{"Night Ferry/Crossing/03 Stern.mp3", "Stern", "Night Ferry", "Crossing"},
		{"Quiet Engine/Idle/01 Choke.mp3", "Choke", "Quiet Engine", "Idle"},
		{"Quiet Engine/Idle/02 Rev.mp3", "Rev", "Quiet Engine", "Idle"},
		{"Quiet Engine/Idle/03 Stall.mp3", "Stall", "Quiet Engine", "Idle"},
	}
	// The seed's artist and album also have tracks only in a playlist the
	// phone does not keep offline. They would score highest, but the phone
	// cannot play them once the desktop is gone.
	deskOnly := []offlineRadioTrack{
		{"Aurora Lane/Tides/03 Undertow.mp3", "Undertow", "Aurora Lane", "Tides"},
		{"Aurora Lane/Tides/04 Riptide.mp3", "Riptide", "Aurora Lane", "Tides"},
	}
	files := map[string][]byte{}
	for i, tr := range append(append([]offlineRadioTrack{}, roadTrip...), deskOnly...) {
		files[tr.title] = desktop.addTaggedFile(tr, 4096+64*i)
	}
	if err := desktop.rt.files.ScanAndSync(t.Context()); err != nil {
		t.Fatal(err)
	}

	var trip, desk core.SyncedPlaylistDetail
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Road trip"}, &trip, http.StatusCreated)
	for _, tr := range roadTrip {
		desktop.must(http.MethodPost, "/playlists/"+trip.ID+"/tracks", trackSong(tr), nil, http.StatusOK)
	}
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Desk"}, &desk, http.StatusCreated)
	for _, tr := range deskOnly {
		desktop.must(http.MethodPost, "/playlists/"+desk.ID+"/tracks", trackSong(tr), nil, http.StatusOK)
	}
	converge(t, desktop, phone, "both playlists reach the phone", func() bool {
		a, ok1 := phone.playlist(trip.ID)
		b, ok2 := phone.playlist(desk.ID)
		return ok1 && ok2 && len(a.Tracks) == len(roadTrip) && len(b.Tracks) == len(deskOnly)
	})

	phone.must(http.MethodPut, "/offline-set/"+trip.ID, map[string]bool{"enabled": true}, nil, http.StatusOK)
	fetchUntil(t, phone, "every offline track reaches the phone", func() bool {
		states := phone.offlineTrackStates(trip.ID)
		for _, tr := range roadTrip {
			if states[tr.title] != offlineset.StateReady {
				return false
			}
		}
		return true
	})
	phone.must(http.MethodPut, "/recommendations/settings", map[string]any{"onlineRecommendations": false}, nil, http.StatusOK)
	if _, online := phone.settings(); online {
		t.Fatal("online recommendations are still on")
	}

	// No internet, no desktop.
	desktop.stop()
	var det core.SyncedPlaylistDetail
	deadline := time.Now().Add(30 * time.Second)
	for {
		det, _ = phone.playlist(trip.ID)
		playable := 0
		for _, tr := range det.Tracks {
			if tr.State == core.CoverageFull && tr.LibraryTrack != nil {
				playable++
			}
		}
		if playable == len(roadTrip) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("offline playlist never became playable on the phone: %+v", det.Tracks)
		}
		time.Sleep(250 * time.Millisecond)
	}

	var radio struct {
		Available bool                  `json:"available"`
		Offline   bool                  `json:"offline"`
		Tracks    []core.ExternalResult `json:"tracks"`
	}
	seed := roadTrip[0]
	phone.must(http.MethodPost, "/recommendations/radio",
		map[string]any{"seeds": []map[string]string{{"artist": seed.artist, "title": seed.title}}}, &radio, http.StatusOK)
	if !radio.Available || !radio.Offline {
		t.Fatalf("radio available=%v offline=%v, want an offline result", radio.Available, radio.Offline)
	}
	offline := map[string]bool{}
	for _, tr := range roadTrip {
		offline[tr.title] = true
	}
	for _, tr := range radio.Tracks {
		if tr.Title == seed.title {
			t.Fatalf("radio repeats its seed: %+v", radio.Tracks)
		}
		if !offline[tr.Title] {
			t.Fatalf("radio offers %q by %s, which the phone cannot play offline", tr.Title, tr.Artist)
		}
		if tr.Source != "library" || tr.Match == nil || tr.Match.Status != core.MatchInLibrary || tr.ExternalID == "" {
			t.Fatalf("radio track %q is not the phone's library copy: %+v", tr.Title, tr)
		}
	}
	if len(radio.Tracks) < 3 {
		t.Fatalf("radio lined up %d tracks, want at least three: %+v", len(radio.Tracks), radio.Tracks)
	}
	// The seed's album-mate is the closest track the phone holds.
	if radio.Tracks[0].Title != "Harbor" {
		t.Fatalf("radio leads with %q, want the seed's album-mate Harbor", radio.Tracks[0].Title)
	}

	// Radio queues its next three behind the seed, as a Radio session does,
	// and each one plays from the phone's own copy.
	seedID := ""
	for _, tr := range det.Tracks {
		if tr.Title == seed.title {
			seedID = tr.LibraryTrack.ID
		}
	}
	player := func(id, title, artist, album string) map[string]any {
		return map[string]any{"id": id, "title": title, "artist": artist, "album": album}
	}
	phone.must(http.MethodPost, "/player/phone/play",
		map[string]any{"tracks": []any{player(seedID, seed.title, seed.artist, seed.album)}}, nil, http.StatusOK)
	var ahead []any
	for _, tr := range radio.Tracks[:3] {
		ahead = append(ahead, player(tr.ExternalID, tr.Title, tr.Artist, tr.Album))
	}
	var queue struct {
		Entries []struct {
			Origin string `json:"origin"`
			Track  struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"track"`
		} `json:"entries"`
		UpNext []int `json:"upNext"`
	}
	phone.must(http.MethodPost, "/player/phone/enqueue", map[string]any{"tracks": ahead, "origin": "radio"}, &queue, http.StatusOK)
	if len(queue.UpNext) != 3 {
		t.Fatalf("queue has %d tracks up next, want Radio's three: %+v", len(queue.UpNext), queue)
	}
	type played struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
		SHA256 string `json:"sha256"`
	}
	var artifact struct {
		Seed   string   `json:"seed"`
		Radio  []string `json:"radio"`
		Queued []played `json:"queued"`
	}
	artifact.Seed = seed.artist + " - " + seed.title
	for _, tr := range radio.Tracks {
		artifact.Radio = append(artifact.Radio, tr.Artist+" - "+tr.Title)
	}
	for _, pos := range queue.UpNext {
		e := queue.Entries[pos]
		if e.Origin != "radio" {
			t.Fatalf("queued %q as %s, want radio", e.Track.Title, e.Origin)
		}
		resp, err := phone.srv.Client().Get(phone.srv.URL + "/api/v1/stream/" + e.Track.ID)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !bytes.Equal(body, files[e.Track.Title]) {
			t.Fatalf("stream %q: status %d, %d bytes, want the offline file", e.Track.Title, resp.StatusCode, len(body))
		}
		sum := sha256.Sum256(body)
		var artist string
		for _, tr := range roadTrip {
			if tr.title == e.Track.Title {
				artist = tr.artist
			}
		}
		artifact.Queued = append(artifact.Queued, played{e.Track.Title, artist, hex.EncodeToString(sum[:])})
	}
	writeE2EArtifact(t, "phone-offline-radio.json", artifact)
}

// writeE2EArtifact records what an end-to-end run observed, as indented JSON,
// in REVERB_E2E_ARTIFACTS when set and the test's temporary directory
// otherwise. The content is deterministic, so two runs can be diffed.
func writeE2EArtifact(t *testing.T, name string, v any) {
	t.Helper()
	dir := os.Getenv("REVERB_E2E_ARTIFACTS")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("artifact: %s", p)
}
