package p2p

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uhhhm/reverb/internal/portablemigrate"
	"github.com/uhhhm/reverb/internal/portablename"
)

// A track stuck because its name cannot be stored on a peer replicates once the
// device that holds it has migrated.
//
// The assertion is made on the serving side on purpose. The setup — a library
// file called "Where Is My Mind?.flac" — cannot exist on Windows at all, so a
// Windows runner could never build the "before" state. What a Windows peer
// actually needs is for the manifest this device advertises to carry a storable
// path and the same content hash it had before, and that is checkable here.
func TestMigrationUnsticksAFileForAPeerThatCouldNotStoreIt(t *testing.T) {
	if !portablename.LocallyStorable("Pixies/Where Is My Mind?.flac") {
		t.Skip("this filesystem cannot create the legacy name the migration exists to fix")
	}
	ctx := context.Background()
	serverDir, clientDir := t.TempDir(), t.TempDir()
	body := []byte("AUDIO BYTES")
	const legacy = "Pixies/Where Is My Mind?.flac"
	writeTrack(t, serverDir, legacy, body)

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
	before, err := serverQ.ListFileManifests(ctx)
	if err != nil || len(before) != 1 {
		t.Fatalf("expected one manifest entry before migrating, got %+v (%v)", before, err)
	}
	hash := before[0].ContentHash
	if portablename.IsPortable(before[0].RelPath) {
		t.Fatalf("the test's own legacy name is already portable: %q", before[0].RelPath)
	}

	res, err := portablemigrate.New(serverDir, nil).WithManifest(serverQ, "server-device").Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Renamed) != 1 {
		t.Fatalf("want the track renamed, got %+v", res.Renamed)
	}

	after, err := serverQ.ListFileManifests(ctx)
	if err != nil || len(after) != 1 {
		t.Fatalf("migration should leave exactly one entry, got %+v (%v)", after, err)
	}
	if !portablename.IsPortable(after[0].RelPath) {
		t.Fatalf("the advertised path is still unstorable on a peer: %q", after[0].RelPath)
	}
	if after[0].ContentHash != hash {
		t.Fatalf("content hash changed across the rename (%q to %q): a peer holding these bytes would re-fetch them", hash, after[0].ContentHash)
	}

	// And the file genuinely replicates under its new name.
	RegisterManifestHandler(serverHost, serverQ, "server-device", serverGuard)
	RegisterFileHandler(serverHost, serverDir, serverGuard)
	clientFS := NewFileSyncer(clientQ, "client-device", clientDir)
	NewPuller(clientHost, clientQ, clientFS, clientGuard, "client-device", clientDir).pullAll(ctx)

	got, err := os.ReadFile(filepath.Join(clientDir, filepath.FromSlash(after[0].RelPath)))
	if err != nil {
		t.Fatalf("the migrated track did not replicate: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("replicated content mismatch: %q", got)
	}
}
