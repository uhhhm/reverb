package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/pyrun/pyruntest"
)

// Production composition, HTTP decoding, real bytes and SQLite faults cover a
// phone restart between required bookkeeping and the terminal commit. The
// counter is written by the controlled downloader, not supplied by the fixture.
func TestDeviceDownloadCompletionSurvivesRecordingAndTerminalWriteFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a phone runtime")
	}
	stub := strings.Replace(stubDownloadingYtDlp, "with open(path, \"wb\") as f:", "with open(path + '.calls', 'a') as counter:\n    counter.write('download\\n')\nwith open(path, \"wb\") as f:", 1)
	py := pyruntest.Host(t, map[string]string{"yt_dlp": stub, "spotdl": "import sys\nsys.exit(1)\n"})
	d := newPhoneDevice(t, "completion-phone", func(d *syncDevice) { d.python = py })
	sql := func(q string) {
		t.Helper()
		if _, err := d.rt.Store.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	sql(`CREATE TRIGGER fail_completion_record BEFORE INSERT ON pending_upload BEGIN SELECT RAISE(FAIL, 'recording unavailable'); END`)
	req := map[string]any{"source": "deezer", "externalId": "completion-fixture", "artist": "Band", "title": "Recoverable song", "album": "Record", "recommendationOrigin": "radio"}
	var job core.DownloadJob
	d.must(http.MethodPost, "/downloads", req, &job, http.StatusOK)
	transitions := []string{"queued"}
	wait := func(predicate func(core.DownloadJob) bool) core.DownloadJob {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			var jobs []core.DownloadJob
			d.must(http.MethodGet, "/downloads", nil, &jobs, http.StatusOK)
			for _, j := range jobs {
				if j.ID == job.ID && predicate(j) {
					return j
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("download did not reach expected phase")
		return core.DownloadJob{}
	}
	pending := wait(func(j core.DownloadJob) bool {
		return j.Status == core.DownloadRunning && j.OutputPath != "" && j.Error != ""
	})
	transitions = append(transitions, "output retained; recording failed")
	if _, err := os.Stat(pending.OutputPath); err != nil {
		t.Fatal(err)
	}
	d.must(http.MethodPost, "/downloads/"+job.ID+"/retry", nil, nil, http.StatusUnprocessableEntity)
	d.must(http.MethodPost, "/downloads", req, nil, http.StatusUnprocessableEntity)
	d.must(http.MethodPost, "/downloads/"+job.ID+"/cancel", nil, nil, http.StatusUnprocessableEntity)
	for _, body := range []any{map[string]any{"ids": []string{job.ID}}, nil} {
		var cleared struct{ Removed int }
		d.must(http.MethodPost, "/downloads/clear", body, &cleared, http.StatusOK)
		if cleared.Removed != 0 {
			t.Fatal("clearing discarded output")
		}
	}
	sql(`CREATE TRIGGER fail_completion_terminal BEFORE UPDATE ON download_jobs WHEN NEW.status = 'completed' BEGIN SELECT RAISE(FAIL, 'terminal write unavailable'); END`)
	sql(`DROP TRIGGER fail_completion_record`)
	d.must(http.MethodPost, "/downloads/"+job.ID+"/retry", nil, nil, http.StatusUnprocessableEntity)
	transitions = append(transitions, "recorded; terminal write failed")
	d.must(http.MethodPost, "/downloads/"+job.ID+"/retry", nil, nil, http.StatusUnprocessableEntity)
	adds, err := d.rt.Store.Q().ListAllRecommendationAdds(context.Background())
	if err != nil || len(adds) != 1 {
		t.Fatalf("repeated completion duplicated attribution: adds=%+v err=%v", adds, err)
	}
	if len(d.pendingUploads()) != 1 {
		t.Fatal("pending upload protection lost")
	}
	d.restart()
	transitions = append(transitions, "device restarted with pending output")
	// The persisted phase must survive even if its error text changes.
	sql(`UPDATE download_jobs SET error = 'Try recording again' WHERE completion_pending = 1`)
	sql(`DROP TRIGGER fail_completion_terminal`)
	completed := wait(func(j core.DownloadJob) bool { return j.Status == core.DownloadCompleted && j.CanonicalID != "" })
	transitions = append(transitions, "completed and linked after automatic recovery")
	calls, err := os.ReadFile(pending.OutputPath + ".calls")
	if err != nil || string(calls) != "download\n" {
		t.Fatalf("downloader reran: %q %v", calls, err)
	}
	if _, err := os.Stat(pending.OutputPath); err != nil {
		t.Fatal(err)
	}
	adds, err = d.rt.Store.Q().ListAllRecommendationAdds(context.Background())
	if err != nil || len(adds) != 1 || len(d.pendingUploads()) != 1 {
		t.Fatalf("durable records: adds=%+v err=%v", adds, err)
	}
	report := map[string]any{"fixture": "synthetic ID3 MP3 via stubDownloadingYtDlp", "transitions": transitions, "downloaderInvocations": 1, "outputExists": true, "pendingUploadRecords": 1, "recommendationAddRecords": len(adds), "status": completed.Status, "linked": completed.CanonicalID != "", "rerun": "CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' go test ./internal/app -run '^TestDeviceDownloadCompletionSurvivesRecordingAndTerminalWriteFailures$' -count=1 -v"}
	data, _ := json.MarshalIndent(report, "", "  ")
	path := os.Getenv("REVERB_COMPLETION_REPORT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "completion-report.json")
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("verification report: %s\n%s", path, data)
}
