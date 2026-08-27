package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
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

func TestUpgradeRejectsNonUpgrade(t *testing.T) {
	mgr := newFakeManager()
	rec := doUpgrade(t, mgr, http.MethodPost, "/api/v1/downloads/upgrade",
		`{"artist":"A","title":"T","quality":"low","currentQuality":"high"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if mgr.enqueueCalls != 0 {
		t.Error("must not enqueue a downgrade")
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
	mgr.jobs["a"] = core.DownloadJob{ID: "a", Status: core.DownloadCompleted, Artist: "A", Title: "Low one", Quality: core.QualityLow}
	mgr.jobs["b"] = core.DownloadJob{ID: "b", Status: core.DownloadCompleted, Artist: "B", Title: "Already high", Quality: core.QualityHigh}
	mgr.jobs["c"] = core.DownloadJob{ID: "c", Status: core.DownloadFailed, Artist: "C", Title: "Failed", Quality: core.QualityLow}
	// No recorded tier: predates the feature, so it is spotDL's old 128k default.
	mgr.jobs["d"] = core.DownloadJob{ID: "d", Status: core.DownloadCompleted, Artist: "D", Title: "Legacy"}

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
