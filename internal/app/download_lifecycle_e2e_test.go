package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/pyrun/pyruntest"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

// controlledYtDlp is stubDownloadingYtDlp steered by files in a control
// directory, one set per title: <title>.started collects each invocation's
// arguments, <title>.fail makes it fail, and <title>.hold holds it after it
// starts until the file is removed. The holds are the barriers the scenario
// coordinates races with.
func controlledYtDlp(control string) string {
	return strings.Replace(stubDownloadingYtDlp, "body = b\"\"", `control = `+strconv.Quote(control)+`
key = os.path.join(control, title)
with open(key + ".started", "a") as f:
    f.write(" ".join(args) + "\n")
import time
while os.path.exists(key + ".hold"):
    time.sleep(0.02)
if os.path.exists(key + ".fail"):
    print("ERROR: [youtube] no video matches", file=sys.stderr)
    sys.exit(1)
body = b""`, 1)
}

// downloadEvents records every download event a device publishes, across
// restarts, as "<status> <topic>" per job.
type downloadEvents struct {
	mu   sync.Mutex
	byID map[string][]string
}

func (e *downloadEvents) follow(t *testing.T, bus *events.Bus) {
	t.Helper()
	for _, topic := range []string{download.TopicQueued, download.TopicProgress, download.TopicComplete, download.TopicFailed, download.TopicRemoved} {
		ch, unsubscribe := bus.Subscribe(topic)
		t.Cleanup(unsubscribe)
		go func() {
			for ev := range ch {
				e.mu.Lock()
				switch p := ev.Payload.(type) {
				case core.DownloadEvent:
					e.byID[p.JobID] = append(e.byID[p.JobID], string(p.Status)+" "+ev.Topic)
				case core.DownloadRemovedEvent:
					for _, id := range p.JobIDs {
						e.byID[id] = append(e.byID[id], "removed "+ev.Topic)
					}
				}
				e.mu.Unlock()
			}
		}()
	}
}

// statuses is the published sequence for a job with repeated statuses collapsed.
func (e *downloadEvents) statuses(id string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.byID[id] {
		status := strings.Fields(ev)[0]
		if len(out) == 0 || out[len(out)-1] != status {
			out = append(out, status)
		}
	}
	return out
}

func (d *syncDevice) jobs() []core.DownloadJob {
	d.t.Helper()
	var jobs []core.DownloadJob
	d.must(http.MethodGet, "/downloads", nil, &jobs, http.StatusOK)
	return jobs
}

func (d *syncDevice) job(id string) (core.DownloadJob, bool) {
	d.t.Helper()
	for _, j := range d.jobs() {
		if j.ID == id {
			return j, true
		}
	}
	return core.DownloadJob{}, false
}

func (d *syncDevice) waitJob(id, what string, cond func(core.DownloadJob, bool) bool) core.DownloadJob {
	d.t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := d.job(id); cond(j, ok) {
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	j, ok := d.job(id)
	d.t.Fatalf("%s: job %s never got there (present=%v, %+v)", what, id, ok, j)
	return core.DownloadJob{}
}

func statusIs(want core.DownloadStatus) func(core.DownloadJob, bool) bool {
	return func(j core.DownloadJob, ok bool) bool { return ok && j.Status == want && !j.CompletionPending }
}

// storedJob is the job's database row, read past the HTTP layer.
type storedJob struct {
	Status            string `json:"status"`
	Attempts          int    `json:"attempts"`
	CompletionPending bool   `json:"completionPending"`
	DownloaderRef     string `json:"downloaderRef,omitempty"`
	Present           bool   `json:"present"`
}

func (d *syncDevice) storedJob(id string) storedJob {
	d.t.Helper()
	var out storedJob
	err := d.rt.Store.DB().QueryRow(`SELECT status, attempts, completion_pending, downloader_ref FROM download_jobs WHERE id = ?`, id).
		Scan(&out.Status, &out.Attempts, &out.CompletionPending, &out.DownloaderRef)
	out.Present = err == nil
	return out
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func invocations(t *testing.T, control, title string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(control, title+".started"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// lifecycleOutcome is one competing-action case in the verification report.
type lifecycleOutcome struct {
	Case        string    `json:"case"`
	Lane        string    `json:"lane"`
	Actions     []string  `json:"actions"`
	Published   []string  `json:"published"`
	Stored      storedJob `json:"stored"`
	Listed      string    `json:"listed"`
	Reconnected string    `json:"listedAfterRestart"`
	Downloads   int       `json:"downloaderInvocations"`
}

func listed(d *syncDevice, id string) string {
	if j, ok := d.job(id); ok {
		return string(j.Status)
	}
	return "absent"
}

func writeLifecycleReport(t *testing.T, name string, outcomes []lifecycleOutcome) {
	t.Helper()
	writeE2EArtifact(t, name, map[string]any{
		"rerun":    "REVERB_E2E_ARTIFACTS=\"$PWD/.scratch/architecture-review\" CGO_CFLAGS='-O2 -g -DHAVE_STRCHRNUL' go test ./internal/app -run 'TestDownloadControlsFollowOneLifecycle' -count=1 -v",
		"outcomes": outcomes,
	})
}

// The synchronous lane on a phone: a cancel that beats the downloader's
// output, a cancel after output reached completion, and a retry the device
// restarts through, each coordinated by holds in the downloader itself.
func TestDownloadControlsFollowOneLifecycleSync(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a phone runtime")
	}
	control := t.TempDir()
	py := pyruntest.Host(t, map[string]string{"yt_dlp": controlledYtDlp(control), "spotdl": "import sys\nsys.exit(1)\n"})
	phone := newPhoneDevice(t, "lifecycle-phone", func(d *syncDevice) { d.python = py })
	evs := &downloadEvents{byID: map[string][]string{}}
	evs.follow(t, phone.rt.Bus)
	enqueue := func(title string) core.DownloadJob {
		var job core.DownloadJob
		phone.must(http.MethodPost, "/downloads", map[string]any{
			"source": "deezer", "externalId": "dz-" + title, "artist": "Band", "title": title, "album": "Record", "recommendationOrigin": "radio",
		}, &job, http.StatusOK)
		return job
	}
	sql := func(q string) {
		t.Helper()
		if _, err := phone.rt.Store.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var outcomes []lifecycleOutcome

	// 1. Cancel while the downloader runs, before it has written anything.
	// A retry is a new attempt that downloads again and completes.
	touch(t, filepath.Join(control, "Held Song.hold"))
	held := enqueue("Held Song")
	waitForFile(t, filepath.Join(control, "Held Song.started"))
	phone.must(http.MethodPost, "/downloads/"+held.ID+"/cancel", nil, nil, http.StatusOK)
	phone.waitJob(held.ID, "cancel", statusIs(core.DownloadCanceled))
	if err := os.Remove(filepath.Join(control, "Held Song.hold")); err != nil {
		t.Fatal(err)
	}
	var retried core.DownloadJob
	phone.must(http.MethodPost, "/downloads/"+held.ID+"/retry", nil, &retried, http.StatusOK)
	if retried.Attempts != 1 || retried.Status != core.DownloadQueued {
		t.Fatalf("retry returned %+v, want queued attempt 1", retried)
	}
	phone.waitJob(held.ID, "retried download", statusIs(core.DownloadCompleted))
	outcomes = append(outcomes, lifecycleOutcome{
		Case: "cancel before output, then retry", Lane: "synchronous (yt-dlp)",
		Actions: []string{"enqueue", "downloader held after start", "cancel", "release", "retry"},
	})

	// 2. Output exists but its record fails: the job is past cancellation.
	// Cancel and clear are refused; once recording works a retry records the
	// same output without downloading again.
	sql(`CREATE TRIGGER fail_lifecycle_record BEFORE INSERT ON pending_upload BEGIN SELECT RAISE(FAIL, 'recording unavailable'); END`)
	recorded := enqueue("Recorded Song")
	phone.waitJob(recorded.ID, "output retained", func(j core.DownloadJob, ok bool) bool {
		return ok && j.Status == core.DownloadRunning && j.OutputPath != "" && j.Error != ""
	})
	phone.must(http.MethodPost, "/downloads/"+recorded.ID+"/cancel", nil, nil, http.StatusUnprocessableEntity)
	phone.must(http.MethodPost, "/downloads/"+recorded.ID+"/clear", nil, nil, http.StatusUnprocessableEntity)
	sql(`DROP TRIGGER fail_lifecycle_record`)
	phone.must(http.MethodPost, "/downloads/"+recorded.ID+"/retry", nil, nil, http.StatusOK)
	phone.waitJob(recorded.ID, "recorded", statusIs(core.DownloadCompleted))
	outcomes = append(outcomes, lifecycleOutcome{
		Case: "cancel after output reached completion", Lane: "synchronous (yt-dlp)",
		Actions: []string{"enqueue", "output written, record refused", "cancel (refused)", "clear (refused)", "record restored", "retry"},
	})

	// 3. Retry through a restart: the failed job is retried with a manual URL
	// while dispatch is paused, the device restarts, and recovery runs the
	// retry with that URL and the original request.
	touch(t, filepath.Join(control, "Retried Song.fail"))
	failing := enqueue("Retried Song")
	phone.waitJob(failing.ID, "first attempt fails", statusIs(core.DownloadFailed))
	if err := os.Remove(filepath.Join(control, "Retried Song.fail")); err != nil {
		t.Fatal(err)
	}
	phone.must(http.MethodPost, "/downloads/pause", nil, nil, http.StatusOK)
	const manual = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	phone.must(http.MethodPost, "/downloads/"+failing.ID+"/retry", map[string]string{"manualUrl": manual}, &retried, http.StatusOK)
	if stored := phone.storedJob(failing.ID); stored.Status != "queued" || stored.Attempts != 1 {
		t.Fatalf("retry persisted %+v, want queued attempt 1", stored)
	}
	beforeRestart := map[string]string{}
	for _, id := range []string{held.ID, recorded.ID, failing.ID} {
		beforeRestart[id] = listed(phone, id)
	}
	phone.restart()
	evs.follow(t, phone.rt.Bus)
	phone.waitJob(failing.ID, "retry after restart", statusIs(core.DownloadCompleted))
	calls := invocations(t, control, "Retried Song")
	if len(calls) != 2 || !strings.Contains(calls[1], manual) || strings.Contains(calls[0], manual) {
		t.Fatalf("invocations %q, want the retry alone to use the manual URL", calls)
	}
	outcomes = append(outcomes, lifecycleOutcome{
		Case: "retry with a manual URL across a restart", Lane: "synchronous (yt-dlp)",
		Actions: []string{"enqueue", "downloader fails", "pause", "retry with manual URL", "restart", "recovery dispatches"},
	})

	// Every job: the database, the job list and the events agree, and a client
	// reconnecting after the restart sees the same final state.
	for i, id := range []string{held.ID, recorded.ID, failing.ID} {
		title := []string{"Held Song", "Recorded Song", "Retried Song"}[i]
		stored := phone.storedJob(id)
		published := evs.statuses(id)
		if stored.Status != "completed" || listed(phone, id) != "completed" || published[len(published)-1] != "completed" {
			t.Errorf("%s: stored %+v, listed %s, published %v", title, stored, listed(phone, id), published)
		}
		outcomes[i].Published, outcomes[i].Stored, outcomes[i].Listed = published, stored, beforeRestart[id]
		outcomes[i].Reconnected = listed(phone, id)
		outcomes[i].Downloads = len(invocations(t, control, title))
	}
	if outcomes[0].Downloads != 2 || outcomes[1].Downloads != 1 || outcomes[2].Downloads != 2 {
		t.Errorf("downloader invocations %d/%d/%d, want 2/1/2", outcomes[0].Downloads, outcomes[1].Downloads, outcomes[2].Downloads)
	}
	if got := evs.statuses(held.ID); len(got) < 3 || !slices.Equal(got[:3], []string{"queued", "running", "canceled"}) {
		t.Errorf("held job published %v, want it canceled before its retry", got)
	}
	if stored := phone.storedJob(held.ID); stored.Attempts != 1 {
		t.Errorf("held job attempts %d, want 1", stored.Attempts)
	}

	// Clearing finished jobs removes exactly those, and nothing comes back.
	touch(t, filepath.Join(control, "Active Song.hold"))
	active := enqueue("Active Song")
	waitForFile(t, filepath.Join(control, "Active Song.started"))
	var cleared struct{ Removed int }
	phone.must(http.MethodPost, "/downloads/clear", nil, &cleared, http.StatusOK)
	if cleared.Removed != 3 || listed(phone, active.ID) != "running" {
		t.Fatalf("cleared %d, active job %s; want the three finished jobs only", cleared.Removed, listed(phone, active.ID))
	}
	_ = os.Remove(filepath.Join(control, "Active Song.hold"))
	phone.waitJob(active.ID, "active job", statusIs(core.DownloadCompleted))
	for _, id := range []string{held.ID, recorded.ID, failing.ID} {
		if stored := phone.storedJob(id); stored.Present {
			t.Errorf("cleared job %s is back: %+v", id, stored)
		}
	}
	writeLifecycleReport(t, "download-lifecycle-sync.json", outcomes)
}

// fakeLidarr answers the Lidarr API the adapter uses. Its album search can be
// held, so a submission is still in flight while the owner cancels and clears.
type fakeLidarr struct {
	mu         sync.Mutex
	nextAlbum  int
	albums     map[string]int // title -> album id
	complete   map[int]bool
	monitored  map[int]bool
	unmonitors []int
	holdSearch chan struct{} // non-nil: the album search waits for it to close
	searching  chan struct{}
}

func newFakeLidarr(t *testing.T) (*fakeLidarr, *httptest.Server) {
	l := &fakeLidarr{nextAlbum: 76, albums: map[string]int{}, complete: map[int]bool{}, monitored: map[int]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(l.serve))
	t.Cleanup(srv.Close)
	return l, srv
}

func (l *fakeLidarr) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	write := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	switch {
	case path == "/album/lookup":
		term := r.URL.Query().Get("term")
		title := strings.TrimPrefix(term, "Band ")
		write([]map[string]any{{"title": title, "foreignAlbumId": "fa-" + title, "artist": map[string]any{"artistName": "Band", "foreignArtistId": "fa-band"}}})
	case path == "/artist" && r.Method == http.MethodGet:
		write([]map[string]any{{"id": 5, "artistName": "Band", "foreignArtistId": "fa-band"}})
	case path == "/album" && r.Method == http.MethodGet:
		l.mu.Lock()
		var out []map[string]any
		for title, id := range l.albums {
			out = append(out, map[string]any{"id": id, "title": title, "foreignAlbumId": "fa-" + title})
		}
		l.mu.Unlock()
		write(out)
	case path == "/album/monitor":
		var body struct {
			AlbumIDs  []int `json:"albumIds"`
			Monitored bool  `json:"monitored"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		l.mu.Lock()
		for _, id := range body.AlbumIDs {
			l.monitored[id] = body.Monitored
			if !body.Monitored {
				l.unmonitors = append(l.unmonitors, id)
			}
		}
		l.mu.Unlock()
		write(map[string]any{})
	case path == "/command":
		l.mu.Lock()
		hold, searching := l.holdSearch, l.searching
		l.mu.Unlock()
		if hold != nil {
			close(searching)
			<-hold
		}
		write(map[string]any{"id": 1})
	case strings.HasPrefix(path, "/album/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/album/"))
		l.mu.Lock()
		done := l.complete[id]
		l.mu.Unlock()
		files := 0
		if done {
			files = 10
		}
		write(map[string]any{"id": id, "statistics": map[string]any{"trackCount": 10, "trackFileCount": files}})
	case path == "/queue":
		write(map[string]any{"records": []any{}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// addAlbum gives Lidarr an album id for a title, as adding its artist would.
func (l *fakeLidarr) addAlbum(title string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextAlbum++
	l.albums[title] = l.nextAlbum
	return l.nextAlbum
}

func (l *fakeLidarr) unmonitored() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]int(nil), l.unmonitors...)
}

// The asynchronous lane on a desktop. No HTTP route enqueues an album, so the
// album jobs are rows an earlier session left queued: boot recovery submits
// them to Lidarr. A submission still in flight when its job is canceled and
// cleared, and an import Lidarr finishes after a cancel, must not bring
// either job back.
func TestDownloadControlsFollowOneLifecycleAsync(t *testing.T) {
	if testing.Short() {
		t.Skip("boots a desktop runtime")
	}
	lidarr, lidarrSrv := newFakeLidarr(t)
	heldAlbum, lateAlbum := lidarr.addAlbum("Held Record"), lidarr.addAlbum("Late Record")
	release := make(chan struct{})
	lidarr.holdSearch, lidarr.searching = release, make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	jobs := map[string]string{"Held Record": "album-held", "Late Record": "album-late"}
	desktop := newSyncDevice(t, "lifecycle-desktop", func(d *syncDevice) {
		d.prepare = func(st *store.Store) {
			ctx := context.Background()
			cfg, _ := json.Marshal(map[string]any{"url": lidarrSrv.URL, "api_key": "key", "root_folder": "/music", "quality_profile_id": 1})
			if err := st.Q().CreateAdapterInstance(ctx, db.CreateAdapterInstanceParams{
				ID: "lidarr", Type: "downloader", Name: "lidarr", Enabled: 1, ConfigJson: string(cfg),
			}); err != nil {
				t.Fatal(err)
			}
			jobsStore := download.NewSQLStore(st.Q())
			// Only the held one is queued now; the late one is already running at Lidarr.
			for title, id := range jobs {
				req := core.DownloadRequest{Source: "deezer", ExternalID: id, Artist: "Band", Title: title, Album: title, Granularity: core.GranularityAlbum}
				job := core.DownloadJob{ID: id, DedupKey: download.DedupKey(req), Status: core.DownloadQueued, DownloaderName: "lidarr", Source: "deezer", ExternalID: id, Artist: "Band", Title: title, Album: title}
				if err := jobsStore.Insert(ctx, job, req); err != nil {
					t.Fatal(err)
				}
				if title == "Late Record" {
					job.Status, job.DownloaderRef, job.StartedAt = core.DownloadRunning, strconv.Itoa(lateAlbum), time.Now().Unix()
					if err := jobsStore.Update(ctx, job); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	})
	evs := &downloadEvents{byID: map[string][]string{}}
	evs.follow(t, desktop.rt.Bus)

	// The held job's submission reaches Lidarr's album search and waits there.
	select {
	case <-lidarr.searching:
	case <-time.After(30 * time.Second):
		t.Fatal("boot recovery never submitted the queued album")
	}
	desktop.must(http.MethodPost, "/downloads/album-held/cancel", nil, nil, http.StatusOK)
	desktop.must(http.MethodPost, "/downloads/album-held/clear", nil, nil, http.StatusOK)
	releaseOnce.Do(func() { close(release) })
	deadline := time.Now().Add(30 * time.Second)
	for !slices.Contains(lidarr.unmonitored(), heldAlbum) {
		if time.Now().After(deadline) {
			t.Fatalf("the late submission of album %d was never abandoned: unmonitored %v", heldAlbum, lidarr.unmonitored())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if stored := desktop.storedJob("album-held"); stored.Present {
		t.Fatalf("cleared album job is back: %+v", stored)
	}

	// The running album is canceled; Lidarr finishes importing it anyway. A
	// restart and the reconciler that resumes with it must leave it canceled.
	desktop.must(http.MethodPost, "/downloads/album-late/cancel", nil, nil, http.StatusOK)
	desktop.waitJob("album-late", "cancel", statusIs(core.DownloadCanceled))
	lidarr.mu.Lock()
	lidarr.complete[lateAlbum] = true
	lidarr.mu.Unlock()
	beforeRestart := map[string]string{"album-held": listed(desktop, "album-held"), "album-late": listed(desktop, "album-late")}
	desktop.restart()
	evs.follow(t, desktop.rt.Bus)
	reconnected := map[string]string{"album-held": listed(desktop, "album-held"), "album-late": listed(desktop, "album-late")}
	if reconnected["album-held"] != "absent" || reconnected["album-late"] != "canceled" {
		t.Fatalf("after restart: %v", reconnected)
	}
	for id, published := range map[string][]string{"album-held": evs.statuses("album-held"), "album-late": evs.statuses("album-late")} {
		if slices.Contains(published, "running") && id == "album-held" || slices.Contains(published, "completed") {
			t.Errorf("%s published %v", id, published)
		}
	}
	if !slices.Contains(lidarr.unmonitored(), lateAlbum) {
		t.Errorf("canceling album %d did not unmonitor it at Lidarr: %v", lateAlbum, lidarr.unmonitored())
	}
	writeLifecycleReport(t, "download-lifecycle-async.json", []lifecycleOutcome{
		{
			Case: "submission returns after cancel and clear", Lane: "asynchronous (Lidarr)",
			Actions:   []string{"queued row recovered at boot", "Lidarr search held", "cancel", "clear", "release search"},
			Published: evs.statuses("album-held"), Stored: desktop.storedJob("album-held"),
			Listed: beforeRestart["album-held"], Reconnected: reconnected["album-held"],
		},
		{
			Case: "import finishes after cancel", Lane: "asynchronous (Lidarr)",
			Actions:   []string{"running at Lidarr", "cancel", "Lidarr reports the album imported", "restart"},
			Published: evs.statuses("album-late"), Stored: desktop.storedJob("album-late"),
			Listed: beforeRestart["album-late"], Reconnected: reconnected["album-late"],
		},
	})
}
