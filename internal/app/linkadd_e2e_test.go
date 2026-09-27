package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
)

// Add from link into a managed playlist, on a phone whose yt-dlp really
// downloads (see stubDownloadingYtDlp), paired with a desktop. The ways this
// goes wrong: the membership is written in a form the playlist module cannot
// read, so the track is in neither playlist; a batch's concurrent adds lose
// each other's tracks or land in a random order; the completed download joins
// the playlist a second time; or the download mints a second catalog id for
// the track the link already named.

func youtubeLink(id string) string { return "https://www.youtube.com/watch?v=" + id }

// linkedJobs waits until every job is completed, linked to a library track and
// given its catalog id, which is minted a moment after the link is stored.
func (d *syncDevice) linkedJobs(ids ...string) map[string]core.DownloadJob {
	d.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var jobs []core.DownloadJob
		d.must(http.MethodGet, "/downloads", nil, &jobs, http.StatusOK)
		done := map[string]core.DownloadJob{}
		for _, j := range jobs {
			if j.Status == core.DownloadFailed {
				d.t.Fatalf("download %s failed: %s", j.ID, j.Error)
			}
			if j.Status == core.DownloadCompleted && j.LibraryTrackID != "" && j.CanonicalID != "" {
				done[j.ID] = j
			}
		}
		all := true
		for _, id := range ids {
			if _, ok := done[id]; !ok {
				all = false
			}
		}
		if all {
			return done
		}
		time.Sleep(200 * time.Millisecond)
	}
	d.t.Fatalf("downloads %v never reached the library", ids)
	return nil
}

func playlistTitles(det core.SyncedPlaylistDetail) []string {
	out := make([]string, 0, len(det.Tracks))
	for _, tr := range det.Tracks {
		out = append(out, tr.Title)
	}
	return out
}

func TestAddFromLinkJoinsAManagedPlaylistOnBothDevices(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop")
	phone := newDownloadingPhone(t)
	pair(t, desktop, phone)

	// A playlist that already holds a track, so the links land after it.
	var pl core.SyncedPlaylistDetail
	phone.must(http.MethodPost, "/playlists", map[string]any{"name": "Found on YouTube"}, &pl, http.StatusCreated)
	phone.must(http.MethodPost, "/playlists/"+pl.ID+"/tracks", trackBody(1), nil, http.StatusOK)

	var one struct {
		CatalogID string           `json:"catalogId"`
		Job       core.DownloadJob `json:"job"`
	}
	phone.must(http.MethodPost, "/links/add", map[string]any{
		"url": youtubeLink("rvbLinkOne"), "playlistId": pl.ID,
	}, &one, http.StatusOK)
	var batch struct {
		Results []struct {
			CatalogID string           `json:"catalogId"`
			Job       core.DownloadJob `json:"job"`
			Error     string           `json:"error"`
		} `json:"results"`
	}
	phone.must(http.MethodPost, "/links/add-batch", map[string]any{"items": []map[string]any{
		{"url": youtubeLink("rvbLinkTwo"), "playlistId": pl.ID},
		{"url": youtubeLink("rvbLinkThree"), "playlistId": pl.ID},
		{"url": youtubeLink("rvbLinkFour"), "playlistId": pl.ID},
	}}, &batch, http.StatusOK)
	jobIDs := []string{one.Job.ID}
	catalogIDs := map[string]string{one.Job.ID: one.CatalogID}
	for _, r := range batch.Results {
		if r.Error != "" || r.Job.ID == "" {
			t.Fatalf("batch item: %+v", r)
		}
		jobIDs = append(jobIDs, r.Job.ID)
		catalogIDs[r.Job.ID] = r.CatalogID
	}

	jobs := phone.linkedJobs(jobIDs...)
	// The download names the track by the id the link already gave it.
	for id, job := range jobs {
		if want := catalogIDs[id]; want == "" || job.CanonicalID != want {
			t.Errorf("job %s (%s) linked as catalog id %q, link added it as %q", id, job.Title, job.CanonicalID, want)
		}
	}

	want := []string{"Track 1", "YouTube track rvbLinkOne", "YouTube track rvbLinkTwo", "YouTube track rvbLinkThree", "YouTube track rvbLinkFour"}
	det, ok := phone.playlist(pl.ID)
	if !ok || !equalStrings(playlistTitles(det), want) {
		t.Fatalf("phone playlist = %v, want %v", playlistTitles(det), want)
	}
	// The link tracks are downloaded, so they play from the phone's library.
	for _, tr := range det.Tracks[1:] {
		if tr.State != core.CoverageFull || tr.LibraryTrack == nil {
			t.Errorf("phone: %q is not playable from the library: %+v", tr.Title, tr)
		}
	}

	converge(t, phone, desktop, "the link tracks reach the desktop's playlist", func() bool {
		d, ok := desktop.playlist(pl.ID)
		return ok && equalStrings(playlistTitles(d), want)
	})
	// More rounds change nothing: each track is in each playlist once.
	for i := 0; i < 2; i++ {
		converge(t, phone, desktop, "the playlists stay as they are", func() bool { return true })
	}
	type row struct {
		Title, State, CatalogID string
	}
	type download struct {
		Title, LinkCatalogID, DownloadCatalogID string
	}
	var artifact struct {
		Playlists map[string][]row
		Downloads []download
	}
	artifact.Playlists = map[string][]row{}
	for _, d := range []*syncDevice{phone, desktop} {
		got, _ := d.playlist(pl.ID)
		if !equalStrings(playlistTitles(got), want) {
			t.Fatalf("%s playlist after further rounds = %v, want %v", d.name, playlistTitles(got), want)
		}
		for _, tr := range got.Tracks {
			artifact.Playlists[d.name] = append(artifact.Playlists[d.name], row{tr.Title, string(tr.State), tr.CanonicalID})
		}
	}
	for _, id := range jobIDs {
		artifact.Downloads = append(artifact.Downloads, download{jobs[id].Title, catalogIDs[id], jobs[id].CanonicalID})
	}
	writeE2EArtifact(t, "add-from-link-playlist.json", artifact)
}
