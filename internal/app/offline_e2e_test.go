package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/offlineset"
)

// These cases cover a phone's offline set (ADR 0003): the phone keeps only the
// playlists it marked offline, fetched from a desktop through P2P file sync,
// and plays them with the desktop gone.

// taggedMP3 is an ID3v2.3 tag naming the track, then bytes standing in for
// audio. Nothing in Reverb decodes it; the tags are what identify it.
func taggedMP3(title, artist, album string, size int) []byte {
	var body bytes.Buffer
	for id, text := range map[string]string{"TIT2": title, "TPE1": artist, "TALB": album} {
		data := append([]byte{0}, text...)
		body.WriteString(id)
		_ = binary.Write(&body, binary.BigEndian, uint32(len(data)))
		body.Write([]byte{0, 0})
		body.Write(data)
	}
	n := body.Len()
	out := append([]byte{'I', 'D', '3', 3, 0, 0,
		byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}, body.Bytes()...)
	frame := []byte{0xff, 0xfb, 0x90, 0x00}
	for len(out) < size {
		out = append(out, frame...)
	}
	return out
}

// addFile puts a tagged track into the device's music folder and has file sync
// advertise it.
func (d *syncDevice) addFile(rel, title string, size int) []byte {
	d.t.Helper()
	data := taggedMP3(title, "Band", "Record", size)
	p := filepath.Join(d.musicDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		d.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		d.t.Fatal(err)
	}
	if err := d.rt.files.ScanAndSync(context.Background()); err != nil {
		d.t.Fatal(err)
	}
	return data
}

// musicFiles lists the audio files in the device's folder.
func (d *syncDevice) musicFiles() []string {
	d.t.Helper()
	var out []string
	_ = filepath.WalkDir(d.musicDir, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && filepath.Ext(p) == ".mp3" {
			rel, _ := filepath.Rel(d.musicDir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func (d *syncDevice) offlineStatus() offlineset.Status {
	d.t.Helper()
	var st offlineset.Status
	d.must(http.MethodGet, "/offline-set/status", nil, &st, http.StatusOK)
	return st
}

// offlineTrackStates maps each track title of an offline playlist to its state.
func (d *syncDevice) offlineTrackStates(playlistID string) map[string]offlineset.TrackState {
	out := map[string]offlineset.TrackState{}
	for _, p := range d.offlineStatus().Playlists {
		if p.PlaylistID == playlistID {
			for _, t := range p.Tracks {
				out[t.Title] = t.State
			}
		}
	}
	return out
}

// offlinePlaylists is the ids this device lists as offline.
func (d *syncDevice) offlinePlaylists() map[string]bool {
	var rows []struct {
		PlaylistID string `json:"playlistId"`
		Enabled    bool   `json:"enabled"`
	}
	d.must(http.MethodGet, "/offline-set", nil, &rows, http.StatusOK)
	out := map[string]bool{}
	for _, r := range rows {
		if r.Enabled {
			out[r.PlaylistID] = true
		}
	}
	return out
}

// fetchUntil runs file rounds on the phone until cond holds.
func fetchUntil(t *testing.T, phone *syncDevice, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		phone.rt.P2PPuller.PullNow(context.Background())
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s: phone status %+v, files %v", what, phone.offlineStatus(), phone.musicFiles())
}

func songBody(title string) map[string]any {
	return map[string]any{
		"source": "deezer", "externalId": "dz-" + title, "title": title, "artist": "Band", "album": "Record",
		"durationMs": 180000, "download": false,
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPhoneKeepsItsOfflineSet(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop", withBuiltInLibrary)
	phone := newPhoneDevice(t, "phone")
	pair(t, desktop, phone)

	one := desktop.addFile("Band/Record/01 One.mp3", "One", 4096)
	desktop.addFile("Band/Record/02 Two.mp3", "Two", 4096)
	three := desktop.addFile("Band/Record/03 Three.mp3", "Three", 8192)
	desktop.addFile("Band/Record/04 Elsewhere.mp3", "Elsewhere", 4096)

	var commute, desk core.SyncedPlaylistDetail
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Commute"}, &commute, http.StatusCreated)
	desktop.must(http.MethodPost, "/playlists/"+commute.ID+"/tracks", songBody("One"), nil, http.StatusOK)
	desktop.must(http.MethodPost, "/playlists/"+commute.ID+"/tracks", songBody("Two"), nil, http.StatusOK)
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Desk"}, &desk, http.StatusCreated)
	desktop.must(http.MethodPost, "/playlists/"+desk.ID+"/tracks", songBody("Elsewhere"), nil, http.StatusOK)
	converge(t, desktop, phone, "both playlists reach the phone", func() bool {
		c, ok1 := phone.playlist(commute.ID)
		d, ok2 := phone.playlist(desk.ID)
		return ok1 && ok2 && len(c.Tracks) == 2 && len(d.Tracks) == 1
	})

	// Each device marks its own playlist offline.
	phone.must(http.MethodPut, "/offline-set/"+commute.ID, map[string]bool{"enabled": true}, nil, http.StatusOK)
	desktop.must(http.MethodPut, "/offline-set/"+desk.ID, map[string]bool{"enabled": true}, nil, http.StatusOK)

	fetchUntil(t, phone, "the offline playlist's files reach the phone", func() bool {
		s := phone.offlineTrackStates(commute.ID)
		return s["One"] == offlineset.StateReady && s["Two"] == offlineset.StateReady
	})
	if got, want := phone.musicFiles(), []string{"Band/Record/01 One.mp3", "Band/Record/02 Two.mp3"}; !equalStrings(got, want) {
		t.Fatalf("phone holds %v, want only the offline playlist's %v", got, want)
	}
	st := phone.offlineStatus()
	if len(st.Playlists) != 1 || st.Playlists[0].ReadyCount != 2 || st.Playlists[0].Bytes != 8192 || st.UsedBytes != 8192 || st.Full {
		t.Fatalf("phone storage = %+v", st)
	}

	// The selection is each device's own.
	converge(t, desktop, phone, "a sync round passes", func() bool { return true })
	if got := phone.offlinePlaylists(); len(got) != 1 || !got[commute.ID] {
		t.Fatalf("phone offline set = %v, want only %s", got, commute.ID)
	}
	if got := desktop.offlinePlaylists(); len(got) != 1 || !got[desk.ID] {
		t.Fatalf("desktop offline set = %v, want only %s", got, desk.ID)
	}

	// An edit on the desktop reaches the phone's files at its next sync.
	desktop.must(http.MethodPost, "/playlists/"+commute.ID+"/tracks", songBody("Three"), nil, http.StatusOK)
	desktop.must(http.MethodDelete, "/playlists/"+commute.ID+"/tracks?source=deezer&externalId=dz-Two", nil, nil, http.StatusOK)
	converge(t, desktop, phone, "the edit reaches the phone", func() bool {
		c, ok := phone.playlist(commute.ID)
		return ok && len(c.Tracks) == 2 && c.Tracks[1].Title == "Three"
	})
	fetchUntil(t, phone, "the added track arrives and the removed one is pruned", func() bool {
		return equalStrings(phone.musicFiles(), []string{"Band/Record/01 One.mp3", "Band/Record/03 Three.mp3"})
	})

	// With the desktop gone, the phone plays the playlist from its own files.
	desktop.stop()
	want := map[string][]byte{"One": one, "Three": three}
	deadline := time.Now().Add(30 * time.Second)
	for {
		det, _ := phone.playlist(commute.ID)
		playable := 0
		for _, tr := range det.Tracks {
			if tr.State == core.CoverageFull && tr.LibraryTrack != nil {
				playable++
			}
		}
		if playable == 2 {
			for _, tr := range det.Tracks {
				resp, err := phone.srv.Client().Get(phone.srv.URL + "/api/v1/stream/" + tr.LibraryTrack.ID)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK || !bytes.Equal(body, want[tr.Title]) {
					t.Fatalf("stream %q: status %d, %d bytes, want the fetched file", tr.Title, resp.StatusCode, len(body))
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("phone playlist never became playable offline: %+v", det.Tracks)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestPhoneOfflineSetStopsWhenStorageIsFull(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	const size = 64 << 10
	// The phone's disk: whatever its folder holds comes out of capacity.
	capacity := int64(offlineset.DefaultReserve + size + size/2)
	var phone *syncDevice
	disk := func(string) (int64, error) {
		used := int64(0)
		_ = filepath.WalkDir(phone.musicDir, func(_ string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				if info, err := e.Info(); err == nil {
					used += info.Size()
				}
			}
			return nil
		})
		return capacity - used, nil
	}
	desktop := newSyncDevice(t, "desktop", withBuiltInLibrary)
	phone = newPhoneDevice(t, "phone", func(d *syncDevice) { d.freeSpace = disk })
	pair(t, desktop, phone)

	for _, title := range []string{"One", "Two", "Three"} {
		desktop.addFile("Band/Record/"+title+".mp3", title, size)
	}
	var pl core.SyncedPlaylistDetail
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Long flight"}, &pl, http.StatusCreated)
	for _, title := range []string{"One", "Two", "Three"} {
		desktop.must(http.MethodPost, "/playlists/"+pl.ID+"/tracks", songBody(title), nil, http.StatusOK)
	}
	converge(t, desktop, phone, "the playlist reaches the phone", func() bool {
		det, ok := phone.playlist(pl.ID)
		return ok && len(det.Tracks) == 3
	})
	phone.must(http.MethodPut, "/offline-set/"+pl.ID, map[string]bool{"enabled": true}, nil, http.StatusOK)

	fetchUntil(t, phone, "fetching stops at the first track that does not fit", func() bool {
		s := phone.offlineTrackStates(pl.ID)
		return phone.offlineStatus().Full && s["One"] == offlineset.StateReady &&
			s["Two"] == offlineset.StateNoSpace && s["Three"] == offlineset.StateNoSpace
	})
	// More rounds change nothing: no fetch, and no eviction to make room.
	for i := 0; i < 3; i++ {
		phone.rt.P2PPuller.PullNow(context.Background())
	}
	if got := phone.musicFiles(); !equalStrings(got, []string{"Band/Record/One.mp3"}) {
		t.Fatalf("phone holds %v with its storage full, want only the track that fitted", got)
	}

	// Room made elsewhere on the phone lets fetching carry on.
	capacity += 2 * size
	fetchUntil(t, phone, "fetching resumes once there is room", func() bool {
		st := phone.offlineStatus()
		return !st.Full && len(st.Playlists) == 1 && st.Playlists[0].ReadyCount == 3
	})
}
