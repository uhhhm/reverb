package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store"
)

func doUpgrade(t *testing.T, mgr *fakeManager, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv, cookie := downloadTestServer(t, mgr)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// The upgrade path is the only one that may replace an existing file, so it must
// set ForceOverwrite — otherwise both downloaders skip the existing target.
func TestUpgradeForcesOverwrite(t *testing.T) {
	mgr := newFakeManager()
	rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"source":"spotify","externalId":"sp1","artist":"A","title":"T","quality":"best","currentQuality":"low"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if !mgr.lastReq.ForceOverwrite {
		t.Error("upgrade must force overwrite")
	}
	if mgr.lastReq.Quality != core.QualityBest {
		t.Errorf("quality = %q", mgr.lastReq.Quality)
	}
}

// Only a re-fetch at the tier the file already has is refused; it would burn a
// download to produce the same file.
func TestUpgradeRejectsSameTier(t *testing.T) {
	mgr := newFakeManager()
	rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"source":"spotify","externalId":"sp1","artist":"A","title":"T","quality":"high","currentQuality":"high"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if mgr.enqueueCalls != 0 {
		t.Error("must not enqueue a no-op re-fetch")
	}
}

// A deliberate downgrade (to save space) is as valid as an upgrade.
func TestUpgradeAllowsDowngrade(t *testing.T) {
	mgr := newFakeManager()
	rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"source":"spotify","externalId":"sp1","artist":"A","title":"T","quality":"low","currentQuality":"best"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if mgr.lastReq.Quality != core.QualityLow {
		t.Errorf("quality = %q, want low", mgr.lastReq.Quality)
	}
	if !mgr.lastReq.ForceOverwrite {
		t.Error("a downgrade replaces the file too, so it must force overwrite")
	}
}

// The list is symmetric: with a low target, tracks ABOVE it are the ones to act on.
func TestListUpgradableIncludesHigherTierWhenTargetIsLower(t *testing.T) {
	mgr := newFakeManager()
	mgr.jobs["a"] = core.DownloadJob{ID: "a", Source: "spotify", ExternalID: "sp-a", Status: core.DownloadCompleted, Artist: "A", Title: "Best one", Quality: core.QualityBest}
	mgr.jobs["b"] = core.DownloadJob{ID: "b", Source: "spotify", ExternalID: "sp-b", Status: core.DownloadCompleted, Artist: "B", Title: "Already low", Quality: core.QualityLow}

	rec := doUpgrade(t, mgr, http.MethodGet, "/api/v1/downloads/upgradable?quality=low", "")
	var out []upgradableTrack
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "Best one" {
		t.Fatalf("want only the higher-tier track, got %v", out)
	}
}

func TestUpgradeValidatesInput(t *testing.T) {
	mgr := newFakeManager()
	if rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade", `{"artist":"A","quality":"high"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing title: status = %d", rec.Code)
	}
	if rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade", `{"artist":"A","title":"T","quality":"lossless"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad tier: status = %d", rec.Code)
	}
}

func TestListUpgradableFindsCompletedBelowTarget(t *testing.T) {
	mgr := newFakeManager()
	mgr.jobs["a"] = core.DownloadJob{ID: "a", Source: "spotify", ExternalID: "sp-a", Status: core.DownloadCompleted, Artist: "A", Title: "Low one", Quality: core.QualityLow}
	mgr.jobs["b"] = core.DownloadJob{ID: "b", Source: "spotify", ExternalID: "sp-b", Status: core.DownloadCompleted, Artist: "B", Title: "Already high", Quality: core.QualityHigh}
	mgr.jobs["c"] = core.DownloadJob{ID: "c", Source: "spotify", ExternalID: "sp-c", Status: core.DownloadFailed, Artist: "C", Title: "Failed", Quality: core.QualityLow}
	// No recorded tier: predates the feature, so it is spotDL's old 128k default.
	mgr.jobs["d"] = core.DownloadJob{ID: "d", Source: "spotify", ExternalID: "sp-d", Status: core.DownloadCompleted, Artist: "D", Title: "Legacy"}

	rec := doUpgrade(t, mgr, http.MethodGet, "/api/v1/downloads/upgradable?quality=high", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out []upgradableTrack
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, u := range out {
		got[u.Title] = u.Quality
	}
	if len(got) != 2 {
		t.Fatalf("want 2 upgradable, got %d (%v)", len(got), got)
	}
	if got["Low one"] != "low" || got["Legacy"] != "low" {
		t.Errorf("unexpected set: %v", got)
	}
}

// A source-less job cannot be re-fetched: re-running it would be a blind text
// search, which is how an upgrade ends up downloading a different song.
func TestListUpgradableSkipsJobsWithNoSource(t *testing.T) {
	mgr := newFakeManager()
	mgr.jobs["a"] = core.DownloadJob{ID: "a", Status: core.DownloadCompleted, Artist: "A", Title: "01 - Dunanna Pit", Quality: core.QualityLow}

	rec := doUpgrade(t, mgr, http.MethodGet, "/api/v1/downloads/upgradable?quality=high", "")
	var out []upgradableTrack
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("want none, got %v", out)
	}
}

// Without a source the handler must refuse rather than let the downloader guess
// from "<artist> - <title>" and overwrite the file with an unrelated track.
func TestUpgradeRefusesWhenSourceUnknown(t *testing.T) {
	mgr := newFakeManager()
	rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"artist":"A","title":"01 - Dunanna Pit","quality":"high","currentQuality":"low"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if mgr.enqueueCalls != 0 {
		t.Error("must not enqueue a sourceless upgrade")
	}
}

// When the caller does not know the source, it is recovered from the original
// download job so the same recording is re-fetched.
func TestUpgradeRecoversSourceFromHistory(t *testing.T) {
	mgr := newFakeManager()
	mgr.jobs["a"] = core.DownloadJob{
		ID: "a", Source: "spotify", ExternalID: "sp-a", Status: core.DownloadCompleted,
		Artist: "A", Title: "01 - Dunanna Pit", Album: "OST", LibraryTrackID: "lib1", Quality: core.QualityLow,
	}
	rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"libraryTrackId":"lib1","artist":"A","title":"01 - Dunanna Pit","quality":"high","currentQuality":"low"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if mgr.lastReq.Source != "spotify" || mgr.lastReq.ExternalID != "sp-a" {
		t.Errorf("source not recovered: %+v", mgr.lastReq)
	}
}

// trackQualityServer wires the per-track quality store against a real DB, since
// the precedence rule (override → setting → default) is the whole point.
func trackQualityServer(t *testing.T, mgr DownloadManager) (*Server, *http.Cookie) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/quality.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	authSvc, tok := seededAuthToken(t, st)
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts,
		Auth:         authSvc,
		Downloads:    mgr,
		Search:       registry.NewRegistry("search"),
		Downloader:   registry.NewRegistry("downloader"),
		Adapters:     st.Q(),
		TrackQuality: st.Q(),
	})
	return srv, &http.Cookie{Name: sessionCookie, Value: tok}
}

func doQuality(t *testing.T, srv *Server, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.AddCookie(cookie)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestTrackQualityOverrideRoundTrip(t *testing.T) {
	srv, cookie := trackQualityServer(t, newFakeManager())

	// With no override, a track reports the global default.
	rec := doQuality(t, srv, cookie, http.MethodGet, "/api/v1/library/track/t1/quality", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Quality    string `json:"quality"`
		Overridden bool   `json:"overridden"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Quality != string(core.DefaultAudioQuality) || got.Overridden {
		t.Fatalf("unset track = %+v, want the default and overridden=false", got)
	}

	// A downgrade is a legitimate standing preference.
	if rec := doQuality(t, srv, cookie, http.MethodPut, "/api/v1/library/track/t1/quality", `{"quality":"low"}`); rec.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doQuality(t, srv, cookie, http.MethodGet, "/api/v1/library/track/t1/quality", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Quality != "low" || !got.Overridden {
		t.Fatalf("after set = %+v, want low/overridden", got)
	}

	// Empty clears it, falling back to the global setting again.
	if rec := doQuality(t, srv, cookie, http.MethodPut, "/api/v1/library/track/t1/quality", `{"quality":""}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doQuality(t, srv, cookie, http.MethodGet, "/api/v1/library/track/t1/quality", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Overridden {
		t.Fatalf("after clear = %+v, want no override", got)
	}
}

func TestTrackQualityRejectsUnknownTier(t *testing.T) {
	srv, cookie := trackQualityServer(t, newFakeManager())
	if rec := doQuality(t, srv, cookie, http.MethodPut, "/api/v1/library/track/t1/quality", `{"quality":"lossless"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// An upgrade with no explicit tier uses the track's standing override, which is
// what makes the override a preference rather than a one-off.
func TestUpgradeUsesTrackOverrideWhenQualityOmitted(t *testing.T) {
	mgr := newFakeManager()
	srv, cookie := trackQualityServer(t, mgr)
	if rec := doQuality(t, srv, cookie, http.MethodPut, "/api/v1/library/track/lt1/quality", `{"quality":"low"}`); rec.Code != http.StatusOK {
		t.Fatalf("put status = %d: %s", rec.Code, rec.Body.String())
	}
	rec := doQuality(t, srv, cookie, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"source":"spotify","externalId":"sp1","artist":"A","title":"T","libraryTrackId":"lt1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if mgr.lastReq.Quality != core.QualityLow {
		t.Errorf("quality = %q, want the track's override (low)", mgr.lastReq.Quality)
	}
}

// setOverride makes a one-off re-fetch stick as the track's standing quality.
func TestUpgradeWithSetOverridePersists(t *testing.T) {
	mgr := newFakeManager()
	srv, cookie := trackQualityServer(t, mgr)
	rec := doQuality(t, srv, cookie, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"source":"spotify","externalId":"sp1","artist":"A","title":"T","libraryTrackId":"lt1","quality":"medium","setOverride":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = doQuality(t, srv, cookie, http.MethodGet, "/api/v1/library/track/lt1/quality", "")
	var got struct {
		Quality    string `json:"quality"`
		Overridden bool   `json:"overridden"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Quality != "medium" || !got.Overridden {
		t.Fatalf("override = %+v, want medium/overridden", got)
	}
}
