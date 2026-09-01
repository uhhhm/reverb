package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func readQualityIndex(t *testing.T, srv *Server, cookie *http.Cookie) trackQualityIndex {
	t.Helper()
	rec := doQuality(t, srv, cookie, http.MethodGet, "/api/v1/library/track-quality", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d: %s", rec.Code, rec.Body.String())
	}
	var idx trackQualityIndex
	if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
		t.Fatal(err)
	}
	return idx
}

func TestTrackQualityIndexListsOnlyOverrides(t *testing.T) {
	srv, cookie := trackQualityServer(t, newFakeManager())

	idx := readQualityIndex(t, srv, cookie)
	if idx.Default != "high" {
		t.Fatalf("default = %q want high", idx.Default)
	}
	if len(idx.Overrides) != 0 {
		t.Fatalf("overrides = %v want none", idx.Overrides)
	}

	rec := doQuality(t, srv, cookie, http.MethodPut, "/api/v1/library/track/t1/quality", `{"quality":"low"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set status = %d: %s", rec.Code, rec.Body.String())
	}

	idx = readQualityIndex(t, srv, cookie)
	if idx.Overrides["t1"] != "low" {
		t.Fatalf("overrides = %v want t1=low", idx.Overrides)
	}
	// A track that follows the default is absent rather than present with the
	// default value, so the map stays the size of what the user changed.
	if _, ok := idx.Overrides["t2"]; ok {
		t.Fatalf("overrides = %v want no entry for an untouched track", idx.Overrides)
	}
}

func TestBatchTrackQualityAppliesAndClears(t *testing.T) {
	srv, cookie := trackQualityServer(t, newFakeManager())

	rec := doQuality(t, srv, cookie, http.MethodPost, "/api/v1/library/quality/batch",
		`{"trackIds":["t1","t2","t3"],"quality":"medium"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("batch status = %d: %s", rec.Code, rec.Body.String())
	}
	var resp batchRenameResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Applied != 3 || len(resp.Errors) != 0 {
		t.Fatalf("applied = %d errors = %v want 3 and none", resp.Applied, resp.Errors)
	}
	idx := readQualityIndex(t, srv, cookie)
	for _, id := range []string{"t1", "t2", "t3"} {
		if idx.Overrides[id] != "medium" {
			t.Fatalf("overrides = %v want %s=medium", idx.Overrides, id)
		}
	}

	// A blank quality is how "follow the default again" travels.
	rec = doQuality(t, srv, cookie, http.MethodPost, "/api/v1/library/quality/batch",
		`{"trackIds":["t1","t2"],"quality":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d: %s", rec.Code, rec.Body.String())
	}
	idx = readQualityIndex(t, srv, cookie)
	if _, ok := idx.Overrides["t1"]; ok {
		t.Fatalf("overrides = %v want t1 cleared", idx.Overrides)
	}
	if idx.Overrides["t3"] != "medium" {
		t.Fatalf("overrides = %v want t3 untouched", idx.Overrides)
	}
}

func TestBatchTrackQualityRejectsBadInput(t *testing.T) {
	srv, cookie := trackQualityServer(t, newFakeManager())

	cases := []struct {
		name string
		body string
	}{
		{"no ids", `{"trackIds":[],"quality":"low"}`},
		{"unknown quality", `{"trackIds":["t1"],"quality":"lossless"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := doQuality(t, srv, cookie, http.MethodPost, "/api/v1/library/quality/batch", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d want 400: %s", rec.Code, rec.Body.String())
			}
		})
	}

	// Over the batch limit.
	ids := make([]string, maxBatchItems+1)
	for i := range ids {
		ids[i] = "t"
	}
	body, _ := json.Marshal(batchQualityRequest{TrackIDs: ids, Quality: "low"})
	rec := doQuality(t, srv, cookie, http.MethodPost, "/api/v1/library/quality/batch", string(body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-limit status = %d want 400", rec.Code)
	}
}

func TestTrackQualityBatchReturns503WithoutStore(t *testing.T) {
	srv, cookie := downloadTestServer(t, newFakeManager())
	rec := doQuality(t, srv, cookie, http.MethodPost, "/api/v1/library/quality/batch",
		`{"trackIds":["t1"],"quality":"low"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d want 503", rec.Code)
	}
	// The index still answers, so the page can render with everything on the
	// default rather than failing to load.
	idx := readQualityIndex(t, srv, cookie)
	if idx.Default != "high" || len(idx.Overrides) != 0 {
		t.Fatalf("index = %+v want the default and no overrides", idx)
	}
}
