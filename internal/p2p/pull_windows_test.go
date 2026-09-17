package p2p

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/store/db"
)

// A Windows device pulling from a Linux or macOS peer meets names it cannot
// write whenever the peer's library predates portable naming. That is knowable
// before any network work, so it has to be recognised when the round's
// candidates are chosen rather than discovered as a failed write at the end of
// a transfer — and the owner has to be told, because a file silently absent
// forever is indistinguishable from one still in flight.
//
// Windows-only because the behaviour under test is Windows's: on Unix these
// names are ordinary and must keep replicating.
func TestPullRecognisesAnUnwritableRemotePathBeforeFetching(t *testing.T) {
	ctx := context.Background()
	clientDir := t.TempDir()

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

	const rel = "Pixies/Where Is My Mind?.flac"
	if err := serverQ.UpsertFileManifest(ctx, db.UpsertFileManifestParams{
		CanonicalID: "server-device:" + rel,
		ContentHash: "unstorable-hash",
		Size:        1,
		RelPath:     rel,
		DeviceID:    "server-device",
	}); err != nil {
		t.Fatal(err)
	}
	RegisterManifestHandler(serverHost, serverQ, "server-device", serverGuard)

	clientFS := NewFileSyncer(clientQ, "client-device", clientDir)
	// No file handler is registered on the server: reaching the network at all
	// would be the failure this test is about.
	puller := NewPuller(clientHost, clientQ, clientFS, clientGuard, "client-device", clientDir)
	puller.pullAll(ctx)

	row, err := clientQ.GetFileFetchFailure(ctx, db.GetFileFetchFailureParams{
		PeerID:      serverHost.ID().String(),
		ContentHash: "unstorable-hash",
	})
	if err != nil {
		t.Fatalf("the owner has no record of the file that cannot replicate: %v", err)
	}
	if row.Reason != ReasonUnstorablePath {
		t.Fatalf("reason = %q, want %q", row.Reason, ReasonUnstorablePath)
	}
	if row.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0: the path was rejected without a fetch", row.Attempts)
	}
}
