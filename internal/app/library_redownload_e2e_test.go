package app

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// folderSubsonic is a Navidrome that serves whatever is in musicDir, the way
// the bundled one does after a scan: every "Artist - Title.mp3" is a song
// whose id is derived from its path, so a file downloaded again under the
// same name comes back under the same backend id.
func folderSubsonic(t *testing.T, musicDir string) *httptest.Server {
	t.Helper()
	type song struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Artist   string `json:"artist"`
		Album    string `json:"album"`
		Duration int    `json:"duration"`
		Suffix   string `json:"suffix"`
		Path     string `json:"path"`
	}
	songs := func() []song {
		var out []song
		_ = filepath.WalkDir(musicDir, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || filepath.Ext(p) != ".mp3" {
				return nil
			}
			rel, _ := filepath.Rel(musicDir, p)
			rel = filepath.ToSlash(rel)
			artist, title, ok := strings.Cut(strings.TrimSuffix(filepath.Base(rel), ".mp3"), " - ")
			if !ok {
				return nil
			}
			sum := sha1.Sum([]byte(rel))
			out = append(out, song{
				ID: "f-" + hex.EncodeToString(sum[:6]), Title: title, Artist: artist, Album: "Record",
				Duration: 180, Suffix: "mp3", Path: rel,
			})
			return nil
		})
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		return out
	}
	ok := func(w http.ResponseWriter, body map[string]any) {
		body["status"], body["version"] = "ok", "1.16.1"
		_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": body})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch filepath.Base(r.URL.Path) {
		case "search3":
			// The library is a handful of songs: every query sees all of them
			// and the matcher picks.
			all := songs()
			offset, _ := strconv.Atoi(q.Get("songOffset"))
			if offset > len(all) {
				offset = len(all)
			}
			all = all[offset:]
			if n, err := strconv.Atoi(q.Get("songCount")); err == nil && n < len(all) {
				all = all[:n]
			}
			ok(w, map[string]any{"searchResult3": map[string]any{"song": all}})
		case "getSong":
			for _, s := range songs() {
				if s.ID == q.Get("id") {
					ok(w, map[string]any{"song": s})
					return
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"subsonic-response": map[string]any{
				"status": "failed", "version": "1.16.1", "error": map[string]any{"code": 70, "message": "not found"},
			}})
		case "stream":
			for _, s := range songs() {
				if s.ID == q.Get("id") {
					w.Header().Set("Content-Type", "audio/mpeg")
					http.ServeFile(w, r, filepath.Join(musicDir, filepath.FromSlash(s.Path)))
					return
				}
			}
			http.NotFound(w, r)
		case "getPlaylists":
			ok(w, map[string]any{"playlists": map[string]any{"playlist": []any{}}})
		case "getArtists":
			ok(w, map[string]any{"artists": map[string]any{"index": []any{}}})
		case "getAlbumList2":
			ok(w, map[string]any{"albumList2": map[string]any{"album": []any{}}})
		case "getScanStatus", "startScan":
			ok(w, map[string]any{"scanStatus": map[string]any{"scanning": false, "count": len(songs())}})
		default:
			ok(w, map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withDownloadingDesktop boots a built-in desktop whose Navidrome reflects its
// music folder and whose bundled yt-dlp downloads with the stub. spotDL fails,
// so every download falls back to yt-dlp.
func withDownloadingDesktop(d *syncDevice) {
	d.builtIn = true
	d.backend = folderSubsonic
	dir := d.t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/usr/bin/env python3\n"+body), 0o755); err != nil {
			d.t.Fatal(err)
		}
		return p
	}
	// Each download is a fresh encode, so the same track downloaded twice is
	// not byte-identical: yt-dlp embeds a new thumbnail and metadata.
	ytdlp := strings.Replace(stubDownloadingYtDlp, `f.write(tag + b"\xff\xfb\x90\x00" * 1024)`,
		`f.write(tag + b"\xff\xfb\x90\x00" * 1024 + os.urandom(16))`, 1)
	if ytdlp == stubDownloadingYtDlp {
		d.t.Fatal("the yt-dlp stub no longer writes the file this option varies")
	}
	d.env = map[string]string{
		"REVERB_YTDLP_PATH":  write("yt-dlp", ytdlp),
		"REVERB_SPOTDL_PATH": write("spotdl", "import sys\nsys.exit(1)\n"),
	}
}

// browsable is the device's household browsing: title by catalog id.
func (d *syncDevice) browsable() map[string]string {
	d.t.Helper()
	var rows []core.CatalogLibraryTrack
	d.must(http.MethodGet, "/library/catalog/tracks", nil, &rows, http.StatusOK)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Title
	}
	return out
}

// browsingStage is what each device browses at one step of the scenario, by
// title, so the artifact is the same on every run.
type browsingStage struct {
	Stage   string   `json:"stage"`
	Desktop []string `json:"desktop"`
	Phone   []string `json:"phone"`
}

func snapshotBrowsing(stage string, desktop, phone *syncDevice) browsingStage {
	titles := func(d *syncDevice) []string {
		out := []string{}
		for _, title := range d.browsable() {
			out = append(out, title)
		}
		sort.Strings(out)
		return out
	}
	return browsingStage{Stage: stage, Desktop: titles(desktop), Phone: titles(phone)}
}

// A deleted library track that is downloaded again is library again, on every
// device, under the identity it always had, and an edit made to it afterwards
// replicates. A deleted track nobody downloads again stays gone.
func TestRedownloadedTrackReturnsToHouseholdBrowsing(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop", withDownloadingDesktop)
	phone := newPhoneDevice(t, "phone")
	pair(t, desktop, phone)

	again := desktop.download("Again")
	gone := desktop.download("Gone")
	if again.CanonicalID == gone.CanonicalID {
		t.Fatalf("two different tracks share catalog id %s", again.CanonicalID)
	}
	converge(t, desktop, phone, "both downloads are browsable on the phone", func() bool {
		b := phone.browsable()
		return b[again.CanonicalID] == "Again" && b[gone.CanonicalID] == "Gone"
	})
	stages := []browsingStage{snapshotBrowsing("downloaded", desktop, phone)}

	desktop.must(http.MethodDelete, "/library/track/"+again.LibraryTrackID, nil, nil, http.StatusOK)
	desktop.must(http.MethodDelete, "/library/track/"+gone.LibraryTrackID, nil, nil, http.StatusOK)
	converge(t, desktop, phone, "both deleted tracks leave household browsing everywhere", func() bool {
		for _, d := range []*syncDevice{desktop, phone} {
			b := d.browsable()
			if _, ok := b[again.CanonicalID]; ok {
				return false
			}
			if _, ok := b[gone.CanonicalID]; ok {
				return false
			}
		}
		return true
	})
	stages = append(stages, snapshotBrowsing("deleted", desktop, phone))

	if ch, err := desktop.rt.Deps.SyncStore.GetLatestForField(t.Context(), reverbsync.EntityTrack, again.CanonicalID, reverbsync.FieldDeleted); err != nil || ch != nil {
		t.Fatalf("library deletion wrote a track tombstone: %+v, %v", ch, err)
	}

	// The owner finds it again under another source's result. The Deezer job
	// is still listed as completed, so asking for that result again would join
	// it rather than download.
	back := desktop.downloadFrom("spotify", "sp-Again", "Again")
	if back.CanonicalID != again.CanonicalID {
		t.Fatalf("the download came back as %s, want the identity it had, %s", back.CanonicalID, again.CanonicalID)
	}
	if !equalStrings(desktop.musicFiles(), []string{"Band - Again.mp3"}) {
		t.Fatalf("desktop folder holds %v", desktop.musicFiles())
	}
	desktop.must(http.MethodPut, "/library/track/"+back.LibraryTrackID+"/name", map[string]string{
		"title": "Again (Live)", "artist": "Band",
	}, nil, http.StatusOK)

	last := map[string]map[string]string{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last browsed: %v", last)
		}
	})
	converge(t, desktop, phone, "the returned track shows its new name on both devices", func() bool {
		for _, d := range []*syncDevice{desktop, phone} {
			b := d.browsable()
			last[d.name] = b
			if b[again.CanonicalID] != "Again (Live)" {
				return false
			}
			if _, ok := b[gone.CanonicalID]; ok {
				return false
			}
		}
		return true
	})
	// The returned file stays on the desktop through a file round.
	desktop.rt.P2PPuller.PullNow(t.Context())
	if !equalStrings(desktop.musicFiles(), []string{"Band - Again.mp3"}) {
		t.Fatalf("desktop folder holds %v after a file round", desktop.musicFiles())
	}
	stages = append(stages, snapshotBrowsing("downloaded again and renamed", desktop, phone))
	writeE2EArtifact(t, "redownloaded-track-browsing.json", stages)
}
