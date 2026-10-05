package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

// The household's recommendation sources, as the internet answers them. Two
// tracks were played heavily nine days ago; Last.fm relates each to a few
// recordings Deezer can play, and Deezer relates their artists.
type exclusionArtist struct {
	id      int64
	name    string
	related []int64
}

type exclusionTrack struct {
	id     int64
	title  string
	artist int64
}

var (
	exclusionArtists = []exclusionArtist{
		{101, "Aphex Twin", []int64{103, 104, 105}},
		{102, "Boards of Canada", []int64{106, 105}},
		{103, "Squarepusher", nil},
		{104, "Autechre", nil},
		{105, "Plaid", nil},
		{106, "Bonobo", nil},
		{107, "Bola", nil},
	}
	exclusionTracks = []exclusionTrack{
		{1001, "Xtal", 101},
		{1002, "Roygbiv", 102},
		{2001, "Iambic 9 Poetry", 103},
		{2002, "My Red Hot Car", 103},
		{2003, "Bike", 104},
		{2004, "Eyen", 105},
		{2005, "Kiara", 106},
		{2006, "Glink", 107},
	}
	// lastfmSimilar is track.getSimilar by seed title.
	lastfmSimilar = map[string][]int64{
		"xtal":    {2001, 2003, 2004, 2006},
		"roygbiv": {2005, 2002, 2004},
	}
)

const (
	// The current Not interested marks the scenario makes. They are not
	// history: nothing was marked when the surfaces were generated.
	markedArtist     = "Squarepusher"
	markedTrackID    = "2004"
	markedTrackTitle = "Eyen"
)

func exclusionArtistByID(id int64) exclusionArtist {
	for _, a := range exclusionArtists {
		if a.id == id {
			return a
		}
	}
	panic(id)
}

func exclusionTrackByID(id int64) exclusionTrack {
	for _, tr := range exclusionTracks {
		if tr.id == id {
			return tr
		}
	}
	panic(id)
}

// fakeInternet answers Deezer, Last.fm and ListenBrainz in place of the real
// services, and passes loopback traffic (the test's library backend) through.
// Switching it down makes every external request fail as an outage would.
type fakeInternet struct {
	base     http.RoundTripper
	down     atomic.Bool
	answered atomic.Int64
	refused  atomic.Int64
}

func installFakeInternet(t *testing.T) *fakeInternet {
	t.Helper()
	f := &fakeInternet{base: http.DefaultTransport}
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = f.base })
	return f
}

func (f *fakeInternet) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.URL.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return f.base.RoundTrip(r)
	}
	if f.down.Load() {
		f.refused.Add(1)
		return nil, fmt.Errorf("fake internet: %s is unreachable", r.URL.Host)
	}
	f.answered.Add(1)
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	switch r.URL.Hostname() {
	case "api.deezer.com":
		serveDeezer(rec, r)
	case "ws.audioscrobbler.com":
		serveLastfm(rec, r)
	case "api.listenbrainz.org", "labs.api.listenbrainz.org", "musicbrainz.org":
		// ListenBrainz is having a bad day; Last.fm and Deezer carry the scenario.
		rec.WriteHeader(http.StatusServiceUnavailable)
	default:
		rec.WriteHeader(http.StatusNotFound)
	}
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

func deezerTrackJSON(tr exclusionTrack) map[string]any {
	a := exclusionArtistByID(tr.artist)
	return map[string]any{
		"id": tr.id, "title": tr.title, "duration": 200,
		"artist": map[string]any{"id": a.id, "name": a.name},
		"album":  map[string]any{"id": tr.id + 50000, "title": tr.title + " EP"},
	}
}

func deezerArtistJSON(a exclusionArtist) map[string]any {
	return map[string]any{"id": a.id, "name": a.name, "picture_medium": ""}
}

func serveDeezer(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	data := []map[string]any{}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/search/track":
		for _, tr := range exclusionTracks {
			artist := strings.ToLower(exclusionArtistByID(tr.artist).name)
			if q == artist || strings.Contains(q, artist) && strings.Contains(q, strings.ToLower(tr.title)) {
				data = append(data, deezerTrackJSON(tr))
			}
		}
	case r.URL.Path == "/search/artist":
		for _, a := range exclusionArtists {
			if strings.ToLower(a.name) == q {
				data = append(data, deezerArtistJSON(a))
			}
		}
	case len(parts) >= 2 && parts[0] == "artist":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		a := exclusionArtistByID(id)
		if len(parts) == 2 {
			_ = json.NewEncoder(w).Encode(deezerArtistJSON(a))
			return
		}
		if parts[2] == "related" {
			for _, rel := range a.related {
				data = append(data, deezerArtistJSON(exclusionArtistByID(rel)))
			}
		}
		// No new releases: Release Radar answers with a quiet week.
	case len(parts) == 2 && parts[0] == "track":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		_ = json.NewEncoder(w).Encode(deezerTrackJSON(exclusionTrackByID(id)))
		return
	case r.URL.Path == "/search/album":
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "DataException", "message": "no data", "code": 800}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func serveLastfm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !strings.EqualFold(q.Get("method"), "track.getSimilar") {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": 3, "message": "Invalid Method"})
		return
	}
	similar := []map[string]any{}
	for _, id := range lastfmSimilar[strings.ToLower(q.Get("track"))] {
		tr := exclusionTrackByID(id)
		similar = append(similar, map[string]any{
			"name": tr.title, "mbid": "", "duration": "200",
			"artist": map[string]any{"name": exclusionArtistByID(tr.artist).name},
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"similartracks": map[string]any{"track": similar}})
}

// seedListeningHistory writes what the household did before this device
// generated anything: Deezer as a search source, a Last.fm key for
// similarity, and heavy plays of the two seed tracks nine days ago, before
// this week's Discover Weekly period.
func seedListeningHistory(t *testing.T) func(st *store.Store) {
	return func(st *store.Store) {
		ctx := context.Background()
		if err := st.Q().CreateAdapterInstance(ctx, db.CreateAdapterInstanceParams{
			ID: "search-deezer", Type: "search", Name: "deezer", Enabled: 1, ConfigJson: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.Q().UpsertSetting(ctx, db.UpsertSettingParams{Key: "scrobble:lastfm:api_key", Value: "e2e-key"}); err != nil {
			t.Fatal(err)
		}
		played := time.Now().Add(-9 * 24 * time.Hour)
		for i, seed := range []struct {
			id    int64
			plays int
		}{{1001, 4}, {1002, 3}} {
			tr := exclusionTrackByID(seed.id)
			catalogID := fmt.Sprintf("trk_seed_%d", tr.id)
			if _, err := st.DB().ExecContext(ctx, `INSERT INTO catalog_entity (id, kind, title, artist, album, duration_ms, source, external_id, created_at)
				VALUES (?, 'track', ?, ?, ?, 200000, 'deezer', ?, ?)`,
				catalogID, tr.title, exclusionArtistByID(tr.artist).name, tr.title+" EP", strconv.FormatInt(tr.id, 10), played.Unix()); err != nil {
				t.Fatal(err)
			}
			for n := range seed.plays {
				at := played.Add(time.Duration(i*10+n) * time.Minute).Unix()
				if err := st.Q().InsertPlay(ctx, db.InsertPlayParams{
					ID: fmt.Sprintf("play_%d_%d", tr.id, n), UserID: "owner", CatalogID: catalogID,
					PlayedAt: at, MsPlayed: 200000, Completed: 1, CreatedAt: at, Qualified: 1,
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

// surfaceResult is what one surface showed a caller.
type surfaceResult struct {
	Available bool     `json:"available"`
	Offline   bool     `json:"offline,omitempty"`
	UpdatedAt int64    `json:"updatedAt,omitempty"`
	Period    string   `json:"period,omitempty"`
	Items     []string `json:"items"`
}

type recommendationSurfaces struct {
	d        *syncDevice
	playlist string
	raw      map[string]json.RawMessage
}

func trackItems(tracks []core.ExternalResult) []string {
	out := []string{}
	for _, tr := range tracks {
		out = append(out, tr.Artist+" - "+tr.Title)
	}
	return out
}

func (s *recommendationSurfaces) get(name, method, path string, body, out any) {
	s.d.t.Helper()
	var raw json.RawMessage
	s.d.must(method, path, body, &raw, http.StatusOK)
	s.raw[name] = raw
	if err := json.Unmarshal(raw, out); err != nil {
		s.d.t.Fatalf("%s: %v: %s", name, err, raw)
	}
}

// read asks every surface for its result, in the order the report lists them.
func (s *recommendationSurfaces) read() map[string]surfaceResult {
	s.d.t.Helper()
	s.raw = map[string]json.RawMessage{}
	out := map[string]surfaceResult{}
	var tracks struct {
		Available bool                  `json:"available"`
		Offline   bool                  `json:"offline"`
		UpdatedAt int64                 `json:"updatedAt"`
		Tracks    []core.ExternalResult `json:"tracks"`
	}
	trackResult := func() surfaceResult {
		return surfaceResult{Available: tracks.Available, Offline: tracks.Offline, UpdatedAt: tracks.UpdatedAt, Items: trackItems(tracks.Tracks)}
	}
	s.get("similarTracks", http.MethodGet, "/recommendations/similar-tracks?"+url.Values{"artist": {"Aphex Twin"}, "title": {"Xtal"}}.Encode(), nil, &tracks)
	out["similarTracks"] = trackResult()

	var artists struct {
		Available bool                  `json:"available"`
		Offline   bool                  `json:"offline"`
		UpdatedAt int64                 `json:"updatedAt"`
		Artists   []core.ExternalArtist `json:"artists"`
	}
	s.get("similarArtists", http.MethodGet, "/recommendations/artists/deezer/101", nil, &artists)
	names := []string{}
	for _, a := range artists.Artists {
		names = append(names, a.Name)
	}
	out["similarArtists"] = surfaceResult{Available: artists.Available, Offline: artists.Offline, UpdatedAt: artists.UpdatedAt, Items: names}

	var shelves struct {
		Shelves []struct {
			Kind    string                  `json:"kind"`
			Seed    *struct{ Title string } `json:"seed"`
			Tracks  []core.ExternalResult   `json:"tracks"`
			Artists []core.ExternalArtist   `json:"artists"`
		} `json:"shelves"`
		UpdatedAt int64 `json:"updatedAt"`
		Offline   bool  `json:"offline"`
	}
	s.get("shelves", http.MethodGet, "/recommendations/shelves", nil, &shelves)
	shelf := surfaceResult{Available: len(shelves.Shelves) > 0, Offline: shelves.Offline, UpdatedAt: shelves.UpdatedAt, Items: []string{}}
	for _, sh := range shelves.Shelves {
		label := sh.Kind
		if sh.Seed != nil {
			label += "(" + sh.Seed.Title + ")"
		}
		for _, item := range trackItems(sh.Tracks) {
			shelf.Items = append(shelf.Items, label+": "+item)
		}
		for _, a := range sh.Artists {
			shelf.Items = append(shelf.Items, label+": "+a.Name)
		}
	}
	out["shelves"] = shelf

	var mixes struct {
		Mixes []struct {
			Kind      string                `json:"kind"`
			Period    string                `json:"period"`
			Tracks    []core.ExternalResult `json:"tracks"`
			UpdatedAt int64                 `json:"updatedAt"`
			Available bool                  `json:"available"`
			Offline   bool                  `json:"offline"`
		} `json:"mixes"`
	}
	s.get("mixes", http.MethodGet, "/recommendations/mixes", nil, &mixes)
	for _, m := range mixes.Mixes {
		out["mixes/"+m.Kind] = surfaceResult{Available: m.Available, Offline: m.Offline, UpdatedAt: m.UpdatedAt, Period: m.Period, Items: trackItems(m.Tracks)}
	}
	var dw struct {
		Period    string                `json:"period"`
		Tracks    []core.ExternalResult `json:"tracks"`
		UpdatedAt int64                 `json:"updatedAt"`
		Available bool                  `json:"available"`
		Offline   bool                  `json:"offline"`
	}
	s.get("mix/discoverWeekly", http.MethodGet, "/recommendations/mixes/discoverWeekly", nil, &dw)
	out["mix/discoverWeekly"] = surfaceResult{Available: dw.Available, Offline: dw.Offline, UpdatedAt: dw.UpdatedAt, Period: dw.Period, Items: trackItems(dw.Tracks)}

	s.get("playlistSuggestions", http.MethodGet, "/recommendations/playlists/"+s.playlist+"/suggestions", nil, &tracks)
	out["playlistSuggestions"] = trackResult()

	s.get("radio", http.MethodPost, "/recommendations/radio",
		map[string]any{"seeds": []map[string]string{{"artist": "Aphex Twin", "title": "Xtal"}, {"artist": "Boards of Canada", "title": "Roygbiv"}}}, &tracks)
	out["radio"] = trackResult()
	return out
}

// surfacesWithMarks are the surfaces whose warmed result holds both the marked
// artist and the marked track, so their absence later is the marks at work.
var surfacesWithMarks = []string{"similarTracks", "shelves", "mixes/discoverWeekly", "mix/discoverWeekly", "playlistSuggestions", "radio"}

func mentions(items []string, s string) bool {
	for _, item := range items {
		if strings.Contains(item, s) {
			return true
		}
	}
	return false
}

// Every recommendation surface honors the household's current Not interested
// marks, whether its result was just fetched, cached before the mark or kept
// while the sources are down; undo restores from what was kept; and a mark
// store that cannot be read hides every surface without losing what was kept.
func TestRecommendationSurfacesHonorCurrentMarks(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a runtime with a libp2p host")
	}
	internet := installFakeInternet(t)
	desktop := newSyncDevice(t, "desktop", func(d *syncDevice) { d.prepare = seedListeningHistory(t) })
	surfaces := &recommendationSurfaces{d: desktop}

	var playlist core.SyncedPlaylistDetail
	desktop.must(http.MethodPost, "/playlists", map[string]string{"name": "Late night"}, &playlist, http.StatusCreated)
	for _, id := range []int64{1001, 1002} {
		tr := exclusionTrackByID(id)
		desktop.must(http.MethodPost, "/playlists/"+playlist.ID+"/tracks", map[string]any{
			"source": "deezer", "externalId": strconv.FormatInt(tr.id, 10), "title": tr.title,
			"artist": exclusionArtistByID(tr.artist).name, "album": tr.title + " EP", "durationMs": 200000, "download": false,
		}, nil, http.StatusOK)
	}
	surfaces.playlist = playlist.ID

	type phase struct {
		Name     string                   `json:"name"`
		Marks    []string                 `json:"marks"`
		Internet string                   `json:"internet"`
		Surfaces map[string]surfaceResult `json:"surfaces"`
		Checks   []string                 `json:"checks"`
	}
	var phases []phase
	var captured []map[string]json.RawMessage
	record := func(name string, marks []string, got map[string]surfaceResult, checks ...string) {
		internetState := "up"
		if internet.down.Load() {
			internetState = "down"
		}
		phases = append(phases, phase{Name: name, Marks: marks, Internet: internetState, Surfaces: got, Checks: checks})
		captured = append(captured, surfaces.raw)
	}

	// Warm: background generation at boot fills shelves and Discover Weekly.
	var warm map[string]surfaceResult
	deadline := time.Now().Add(60 * time.Second)
	for {
		warm = surfaces.read()
		ready := warm["mix/discoverWeekly"].Period != "" && len(warm["shelves"].Items) > 0
		for _, name := range surfacesWithMarks {
			ready = ready && mentions(warm[name].Items, markedArtist) && mentions(warm[name].Items, markedTrackTitle)
		}
		ready = ready && mentions(warm["similarArtists"].Items, markedArtist)
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("surfaces never warmed with the marked artist and track: %+v", warm)
		}
		time.Sleep(250 * time.Millisecond)
	}
	record("warm", nil, warm, "every surface holds Squarepusher and Eyen")

	// Mark an artist and a track. Every result now comes from what was cached
	// or stored before the marks, so the marks must apply at read time.
	var artistMark, trackMark struct {
		Key string `json:"key"`
	}
	desktop.must(http.MethodPost, "/not-interested", map[string]any{"kind": "artist", "name": markedArtist, "source": "deezer", "id": "103"}, &artistMark, http.StatusOK)
	desktop.must(http.MethodPost, "/not-interested", map[string]any{"kind": "track", "source": "deezer", "externalId": markedTrackID, "title": markedTrackTitle, "artist": "Plaid"}, &trackMark, http.StatusOK)
	marked := surfaces.read()
	marks := []string{"artist:" + markedArtist, "track:deezer:" + markedTrackID}
	assertNoMarked := func(phase string, got map[string]surfaceResult, wantArtist bool) {
		t.Helper()
		for name, res := range got {
			if mentions(res.Items, markedTrackTitle) {
				t.Errorf("%s: %s shows the marked track: %v", phase, name, res.Items)
			}
			if !wantArtist && mentions(res.Items, markedArtist) {
				t.Errorf("%s: %s shows the marked artist: %v", phase, name, res.Items)
			}
		}
	}
	assertNoMarked("marked", marked, false)
	for _, name := range append([]string{"similarArtists"}, surfacesWithMarks...) {
		if len(marked[name].Items) == 0 {
			t.Errorf("marked: %s is empty; only the marked entries should have gone: %+v", name, marked[name])
		}
	}
	if marked["mix/discoverWeekly"].UpdatedAt != warm["mix/discoverWeekly"].UpdatedAt || marked["shelves"].UpdatedAt != warm["shelves"].UpdatedAt {
		t.Errorf("marking regenerated a stored surface: mix %d->%d shelves %d->%d",
			warm["mix/discoverWeekly"].UpdatedAt, marked["mix/discoverWeekly"].UpdatedAt, warm["shelves"].UpdatedAt, marked["shelves"].UpdatedAt)
	}
	record("marked", marks, marked, "no surface shows Squarepusher or Eyen", "stored shelves and Mix were not regenerated")

	// The sources go down. Cached, stored and offline results still honor the marks.
	internet.down.Store(true)
	refusedBefore := internet.refused.Load()
	offline := surfaces.read()
	assertNoMarked("offline", offline, false)
	for _, name := range append([]string{"similarArtists"}, surfacesWithMarks...) {
		if !offline[name].Available || len(offline[name].Items) == 0 {
			t.Errorf("offline: %s has nothing to show from what was kept: %+v", name, offline[name])
		}
	}
	record("offline", marks, offline, "no surface shows Squarepusher or Eyen with every source unreachable")

	// With online recommendations off, similar artists come from the stale
	// cache entry, the explicit offline fallback, and stored surfaces stay.
	desktop.must(http.MethodPut, "/recommendations/settings", map[string]any{"onlineRecommendations": false}, nil, http.StatusOK)
	stale := surfaces.read()
	assertNoMarked("online off", stale, false)
	if res := stale["similarArtists"]; !res.Available || !res.Offline || len(res.Items) == 0 {
		t.Errorf("online off: similar artists %+v, want the stale cache entry without the marked artist", res)
	}
	for _, name := range []string{"shelves", "mix/discoverWeekly"} {
		if len(stale[name].Items) == 0 {
			t.Errorf("online off: %s lost what it stored: %+v", name, stale[name])
		}
	}
	record("online off", marks, stale, "similar artists served from the stale cache without Squarepusher", "stored shelves and Mix still hide Squarepusher and Eyen")
	desktop.must(http.MethodPut, "/recommendations/settings", map[string]any{"onlineRecommendations": true}, nil, http.StatusOK)

	// Undo the artist mark: Squarepusher returns from the kept candidates
	// with the internet still down and nothing regenerated.
	desktop.must(http.MethodDelete, "/not-interested", map[string]string{"key": artistMark.Key}, nil, http.StatusNoContent)
	undone := surfaces.read()
	marks = []string{"track:deezer:" + markedTrackID}
	assertNoMarked("undone", undone, true)
	for _, name := range append([]string{"similarArtists"}, surfacesWithMarks...) {
		if !mentions(undone[name].Items, markedArtist) {
			t.Errorf("undone: %s did not restore Squarepusher from what it kept: %v", name, undone[name].Items)
		}
	}
	if undone["mix/discoverWeekly"].UpdatedAt != warm["mix/discoverWeekly"].UpdatedAt || undone["shelves"].UpdatedAt != warm["shelves"].UpdatedAt {
		t.Error("undo regenerated a stored surface")
	}
	record("undone", marks, undone, "Squarepusher is back on every surface", "Eyen is still hidden", "nothing was regenerated")

	// The mark store fails. No surface may show anything unfiltered.
	if _, err := desktop.rt.Store.DB().Exec(`ALTER TABLE not_interested RENAME TO not_interested_unreadable`); err != nil {
		t.Fatal(err)
	}
	failed := surfaces.read()
	for name, res := range failed {
		if res.Available || len(res.Items) != 0 {
			t.Errorf("marks unreadable: %s returned %+v, want unavailable and empty", name, res)
		}
	}
	record("marks unreadable", marks, failed, "every surface is unavailable and empty")

	// Recovery: everything kept before the failure is still there, unchanged.
	if _, err := desktop.rt.Store.DB().Exec(`ALTER TABLE not_interested_unreadable RENAME TO not_interested`); err != nil {
		t.Fatal(err)
	}
	recovered := surfaces.read()
	for name, before := range undone {
		after := recovered[name]
		if !slices.Equal(after.Items, before.Items) || after.UpdatedAt != before.UpdatedAt || after.Period != before.Period {
			t.Errorf("recovered: %s changed across the failure\nbefore %+v\nafter  %+v", name, before, after)
		}
	}
	record("recovered", marks, recovered, "every surface matches its result from before the failure")
	if internet.refused.Load() == refusedBefore {
		t.Log("no surface asked the internet while it was down")
	}

	writeE2EArtifact(t, "recommendation-exclusions.json", map[string]any{
		"rerun": "REVERB_E2E_ARTIFACTS=\"$PWD/.scratch/architecture-review\" go test ./internal/app -run '^TestRecommendationSurfacesHonorCurrentMarks$' -count=1 -v",
		"fixture": map[string]any{
			"historicalGeneration": map[string]any{
				"plays":             "Aphex Twin - Xtal x4, Boards of Canada - Roygbiv x3, nine days before the run",
				"marksAtGeneration": []string{},
				"sources":           "Last.fm track.getSimilar and Deezer search/related artists; ListenBrainz answers 503",
			},
			"currentExclusions": map[string]any{
				"artist":      markedArtist,
				"track":       "Plaid - " + markedTrackTitle + " (deezer:" + markedTrackID + ")",
				"undo":        "artist mark only",
				"readFailure": "not_interested table renamed away and back",
			},
		},
		"externalRequests": map[string]int64{"answered": internet.answered.Load(), "refusedWhileDown": internet.refused.Load()},
		"phases":           phases,
	})
	writeE2EArtifact(t, "recommendation-exclusions-responses.json", captured)
}
