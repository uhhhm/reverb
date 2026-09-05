package api

import (
	"bytes"
	"encoding/json"
	"github.com/uhhhm/reverb/internal/core"
	"net/http/httptest"
	"os"
	"testing"
)

// The contract runner validates these real handler responses against OpenAPI.
// Ordinary Go tests still check status and forwarding without requiring Node.
func TestDownloadHTTPContract(t *testing.T) {
	mgr := newFakeManager()
	srv, cookie := downloadTestServer(t, mgr)
	samples := map[string]json.RawMessage{}
	request := `{"externalId":"contract","source":"spotify","title":"Title","artist":"Artist","album":"Album","durationMs":1234,"playWhenReady":true,"quality":"high","addToPlaylistId":"pl1","downloader":"ignored"}`
	for _, c := range []struct{ key, method, path, body string }{
		{"create", "POST", "/downloads", request},
		{"list", "GET", "/downloads", ""},
		{"queue", "GET", "/downloads/queue", ""},
		{"retry", "POST", "/downloads/job-contract/retry", ""},
	} {
		req := httptest.NewRequest(c.method, "/api/v1"+c.path, bytes.NewBufferString(c.body))
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", c.key, rec.Code, rec.Body.String())
		}
		samples[c.key] = append(json.RawMessage(nil), rec.Body.Bytes()...)
	}
	if mgr.lastReq.DurationMs != 1234 || mgr.lastReq.AddToPlaylistID != "pl1" || mgr.lastReq.Quality != core.QualityHigh {
		t.Fatalf("request forwarding changed: %+v", mgr.lastReq)
	}
	samples["request"] = json.RawMessage(request)
	events := []wsEnvelope{
		{"download.progress", core.DownloadEvent{JobID: "j", Status: core.DownloadRunning, Progress: -1}},
		{"library.updated", core.LibraryUpdatedEvent{}},
		{"download.queue", core.QueueStateEvent{Paused: true}},
		{"download.removed", core.DownloadRemovedEvent{}},
		{"sync.started", nil},
		{"sync.finished", map[string]any{"durationMs": 12}},
	}
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	samples["events"] = data
	if path := os.Getenv("REVERB_CONTRACT_OUTPUT"); path != "" {
		data, err := json.Marshal(samples)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
