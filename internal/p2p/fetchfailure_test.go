package p2p

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// clockAt returns a fetchFailures bound to a clock the test drives, so backoff
// can be observed without waiting it out.
func newFailures(t *testing.T, now *time.Time) *fetchFailures {
	t.Helper()
	return &fetchFailures{q: newTrustStore(t), now: func() time.Time { return *now }}
}

// one is the candidate most of these tests use: one file, from one peer.
func one(relPath string) candidate {
	return candidate{peerID: "peer-a", contentHash: "hash", relPath: relPath}
}

// A failure that will not change on its own must not be retried at full rate
// every round: the round is capped, so each retry costs a fetchable file its
// slot.
func TestRepeatedFailuresBackOff(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)

	if !f.ready(ctx, one("A/1.flac")) {
		t.Fatal("a file with no history must be attempted")
	}
	f.recordAttempt(ctx, one("A/1.flac"), fmt.Errorf("stream reset"))
	if f.ready(ctx, one("A/1.flac")) {
		t.Fatal("a file that just failed must not be retried in the same round")
	}

	// Each further failure pushes the next attempt further out.
	var prev time.Duration
	for attempt := 1; attempt <= 6; attempt++ {
		row, err := f.q.GetFileFetchFailure(ctx, one("").key())
		if err != nil {
			t.Fatal(err)
		}
		delay := time.Duration(row.NextAttemptAt-row.LastFailedAt) * time.Millisecond
		if attempt > 1 && delay <= prev {
			t.Fatalf("attempt %d waited %s, no longer than the previous %s", attempt, delay, prev)
		}
		prev = delay
		now = now.Add(delay)
		if !f.ready(ctx, one("A/1.flac")) {
			t.Fatalf("attempt %d: the backoff expired but the file is still held back", attempt)
		}
		f.recordAttempt(ctx, one("A/1.flac"), fmt.Errorf("stream reset"))
	}
}

// Backing off is not giving up. A transient failure has to be attempted again
// once conditions may have changed, and the delay has to stay bounded.
func TestBackoffIsCappedAndAlwaysRetriesEventually(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)
	for i := 0; i < 40; i++ {
		f.recordAttempt(ctx, one("A/1.flac"), fmt.Errorf("stream reset"))
	}
	row, err := f.q.GetFileFetchFailure(ctx, one("").key())
	if err != nil {
		t.Fatal(err)
	}
	delay := time.Duration(row.NextAttemptAt-row.LastFailedAt) * time.Millisecond
	if delay != fetchRetryMax {
		t.Fatalf("delay = %s, want it capped at %s", delay, fetchRetryMax)
	}
	now = now.Add(fetchRetryMax)
	if !f.ready(ctx, one("A/1.flac")) {
		t.Fatal("a file must be attempted again once the capped delay has passed")
	}
}

// A file that starts succeeding replicates normally, and a later failure starts
// its backoff from the beginning rather than from where the old one stopped.
func TestSuccessForgetsTheFailureHistory(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)
	for i := 0; i < 5; i++ {
		f.recordAttempt(ctx, one("A/1.flac"), fmt.Errorf("stream reset"))
	}
	f.clear(ctx, one("A/1.flac"))
	if !f.ready(ctx, one("A/1.flac")) {
		t.Fatal("a file with no record must be attempted")
	}
	f.recordAttempt(ctx, one("A/1.flac"), fmt.Errorf("stream reset"))
	row, err := f.q.GetFileFetchFailure(ctx, one("").key())
	if err != nil {
		t.Fatal(err)
	}
	if row.Attempts != 1 {
		t.Fatalf("attempts = %d, want the count to have restarted at 1", row.Attempts)
	}
}

// A peer that renames a file — which is exactly what a portable-name migration
// on that device does — has made it fetchable again. The old path's backoff
// must not hold the new one back.
func TestARenamedRemoteFileIsTriedImmediately(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)
	f.noteUnattempted(ctx, one("A/Where Is My Mind?.flac"), ReasonUnstorablePath, unstorableDetail("A/Where Is My Mind?.flac"))
	for i := 0; i < 5; i++ {
		f.recordAttempt(ctx, one("A/Where Is My Mind?.flac"), fmt.Errorf("stream reset"))
	}
	if f.ready(ctx, one("A/Where Is My Mind?.flac")) {
		t.Fatal("the failing path should still be backed off")
	}
	if !f.ready(ctx, one("A/Where Is My Mind_.flac")) {
		t.Fatal("the peer renamed the file; the new path must be attempted at once")
	}
}

// The selection filter reaches the same conclusion every round. Counting those
// as attempts would inflate the delay for a file nobody tried, and rewriting
// the row each round would make a library full of unstorable names a write
// amplifier.
func TestNoteDoesNotAdvanceTheBackoff(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)
	for i := 0; i < 10; i++ {
		f.noteUnattempted(ctx, one("A/1.flac"), ReasonUnstorablePath, unstorableDetail("A/1.flac"))
	}
	row, err := f.q.GetFileFetchFailure(ctx, one("").key())
	if err != nil {
		t.Fatal(err)
	}
	if row.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0: nothing was attempted", row.Attempts)
	}
	if row.Reason != ReasonUnstorablePath {
		t.Fatalf("reason = %q, want %q", row.Reason, ReasonUnstorablePath)
	}
}

// The owner needs to learn that a file is unfetchable and why. A file silently
// absent forever is worse than one reported as failed, because the owner cannot
// tell it apart from one still in flight.
func TestFailuresAreListedForTheOwner(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)
	f.recordAttempt(ctx, candidate{peerID: "peer-a", contentHash: "hash-a", relPath: "A/1.flac"}, fmt.Errorf("%w for %q: expected x got y", ErrContentMismatch, "A/1.flac"))
	f.noteUnattempted(ctx, candidate{peerID: "peer-a", contentHash: "hash-b", relPath: "B/Etc..flac"}, ReasonUnstorablePath, unstorableDetail("B/Etc..flac"))

	rows, err := f.q.ListFileFetchFailures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want both failures listed, got %d", len(rows))
	}
	byHash := map[string]string{}
	for _, r := range rows {
		byHash[r.ContentHash] = r.Reason
	}
	if byHash["hash-a"] != ReasonContentMismatch {
		t.Fatalf("a hash mismatch should be reported as %q, got %q", ReasonContentMismatch, byHash["hash-a"])
	}
	if byHash["hash-b"] != ReasonUnstorablePath {
		t.Fatalf("an unwritable name should be reported as %q, got %q", ReasonUnstorablePath, byHash["hash-b"])
	}
}

// A store without the capability keeps working: the puller retries everything
// every round, exactly as it did before backoff existed.
func TestFailuresDegradeWithoutTheStore(t *testing.T) {
	ctx := context.Background()
	var f *fetchFailures
	if !f.ready(ctx, one("A/1.flac")) {
		t.Fatal("a nil tracker must not hold anything back")
	}
	f.recordAttempt(ctx, one("A/1.flac"), fmt.Errorf("boom"))
	f.noteUnattempted(ctx, one("A/1.flac"), ReasonUnstorablePath, "")
	f.clear(ctx, one("A/1.flac"))

	empty := &fetchFailures{}
	if !empty.ready(ctx, one("A/1.flac")) {
		t.Fatal("a tracker with no store must not hold anything back")
	}
	empty.recordAttempt(ctx, one("A/1.flac"), fmt.Errorf("boom"))
}

// The urgent case for a mixed-platform household: a peer advertising files this
// device can never write must not stop the ones it can from replicating.
func TestPullCompletesDespiteUnfetchableFiles(t *testing.T) {
	ctx := context.Background()
	serverDir, clientDir := t.TempDir(), t.TempDir()
	body := []byte("AUDIO BYTES")
	writeTrack(t, serverDir, "Artist/Album/01.flac", body)

	serverQ := newTrustStore(t)
	clientQ := newTrustStore(t)
	serverHost, clientHost := newLinkedHosts(t)
	mkDevice(t, serverQ, "server-device")
	mkDevice(t, clientQ, "client-device")

	serverGuard := NewGuard(serverQ)
	if err := serverGuard.Trust(ctx, clientHost.ID(), "", "client"); err != nil {
		t.Fatal(err)
	}
	clientGuard := NewGuard(clientQ)
	if err := clientGuard.Trust(ctx, serverHost.ID(), "", "server"); err != nil {
		t.Fatal(err)
	}

	serverFS := NewFileSyncer(serverQ, "server-device", serverDir)
	if err := serverFS.ScanAndSync(ctx); err != nil {
		t.Fatal(err)
	}
	// Advertise more unfetchable files than one round's budget, sorting ahead of
	// the real file, so a puller that retried them at full rate would spend
	// every slot on them and never reach the file it can actually fetch.
	for i := 0; i < 60; i++ {
		rel := fmt.Sprintf("AAA Ghost/%02d.flac", i)
		if err := serverQ.UpsertFileManifest(ctx, db.UpsertFileManifestParams{
			CanonicalID: "server-device:" + rel,
			ContentHash: fmt.Sprintf("%064d", i),
			Size:        1,
			RelPath:     rel,
			DeviceID:    "server-device",
		}); err != nil {
			t.Fatal(err)
		}
	}
	RegisterManifestHandler(serverHost, serverQ, "server-device", serverGuard)
	RegisterFileHandler(serverHost, serverDir, serverGuard)

	clientFS := NewFileSyncer(clientQ, "client-device", clientDir)
	puller := NewPuller(clientHost, clientQ, clientFS, clientGuard, "client-device", clientDir)
	// Two rounds: the first attempts the ghosts and backs them off, the second
	// must not be crowded out by them.
	puller.pullAll(ctx)
	puller.pullAll(ctx)

	got, err := os.ReadFile(filepath.Join(clientDir, "Artist", "Album", "01.flac"))
	if err != nil {
		t.Fatalf("the fetchable file never replicated: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("replicated content mismatch: %q", got)
	}
	rows, err := clientQ.ListFileFetchFailures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("the owner has no record of which files are not replicating")
	}
}

// A household has several devices. One that no longer holds a file must not
// stop the same content being pulled from a device that does: the reason a
// fetch failed is usually that peer's, not the content's.
func TestABrokenPeerDoesNotHoldBackAHealthyOne(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)

	broken := candidate{peerID: "peer-broken", contentHash: "hash", relPath: "A/1.flac"}
	healthy := candidate{peerID: "peer-healthy", contentHash: "hash", relPath: "A/1.flac"}

	for i := 0; i < 5; i++ {
		f.recordAttempt(ctx, broken, fmt.Errorf("stream reset"))
	}
	if f.ready(ctx, broken) {
		t.Fatal("the failing peer should be backed off")
	}
	if !f.ready(ctx, healthy) {
		t.Fatal("a peer that has never failed must still be asked for the file")
	}
}

// A row is the answer to "why has this track not arrived", so it has to stop
// existing once the track is no longer missing — the content came from another
// device, the owner deleted it, or this peer stopped offering it. A list that
// keeps tracks that are fine is a list the owner learns to distrust.
func TestPruneForgetsWhatIsNoLongerStuck(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0)
	f := newFailures(t, &now)

	stuck := candidate{peerID: "peer-a", contentHash: "still-stuck", relPath: "A/1.flac"}
	arrived := candidate{peerID: "peer-a", contentHash: "arrived-elsewhere", relPath: "A/2.flac"}
	otherPeer := candidate{peerID: "peer-b", contentHash: "arrived-elsewhere", relPath: "A/2.flac"}
	for _, c := range []candidate{stuck, arrived, otherPeer} {
		f.recordAttempt(ctx, c, fmt.Errorf("stream reset"))
	}

	// This peer is still missing only the first one.
	f.prune(ctx, "peer-a", map[string]bool{"still-stuck": true})

	rows, err := f.q.ListFileFetchFailures(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.PeerID+"/"+r.ContentHash] = true
	}
	if !got["peer-a/still-stuck"] {
		t.Error("a file that is still missing must stay on the list")
	}
	if got["peer-a/arrived-elsewhere"] {
		t.Error("a file that is no longer missing must leave the list")
	}
	if !got["peer-b/arrived-elsewhere"] {
		t.Error("pruning one peer must not touch another peer's rows")
	}
}
