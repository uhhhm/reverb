package portablemigrate

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/store/db"
)

// fakeManifest is the file manifest, in memory, keyed the way the real table
// is.
type fakeManifest struct {
	rows map[string]db.FileManifest
}

func newFakeManifest(rows ...db.FileManifest) *fakeManifest {
	m := &fakeManifest{rows: map[string]db.FileManifest{}}
	for _, r := range rows {
		m.rows[r.CanonicalID] = r
	}
	return m
}

func (m *fakeManifest) ListFileManifests(context.Context) ([]db.FileManifest, error) {
	out := make([]db.FileManifest, 0, len(m.rows))
	for _, r := range m.rows {
		out = append(out, r)
	}
	return out, nil
}

func (m *fakeManifest) UpsertFileManifest(_ context.Context, arg db.UpsertFileManifestParams) error {
	m.rows[arg.CanonicalID] = db.FileManifest(arg)
	return nil
}

func (m *fakeManifest) DeleteFileManifest(_ context.Context, canonicalID string) error {
	delete(m.rows, canonicalID)
	return nil
}

// A peer decides what to fetch by comparing content hashes, so the hash has to
// survive the rename: a migration that cost the household a re-transfer of
// every migrated track would be worse than the problem it fixes.
func TestTheManifestFollowsTheRenameWithoutChangingTheContentHash(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Pixies/Where Is My Mind?.flac", "mind")
	manifest := newFakeManifest(db.FileManifest{
		CanonicalID: "dev-1:Pixies/Where Is My Mind?.flac",
		ContentHash: "the-hash",
		Size:        4,
		RelPath:     "Pixies/Where Is My Mind?.flac",
		Mtime:       1234,
		DeviceID:    "dev-1",
	})

	if _, err := New(root, nil).WithManifest(manifest, "dev-1").Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	rows, _ := manifest.ListFileManifests(context.Background())
	if len(rows) != 1 {
		t.Fatalf("want exactly one entry for the file, got %+v", rows)
	}
	got := rows[0]
	if got.RelPath != "Pixies/Where Is My Mind_.flac" {
		t.Errorf("rel path = %q, want the new name", got.RelPath)
	}
	if got.ContentHash != "the-hash" {
		t.Errorf("content hash = %q, want it unchanged: a peer holding these bytes must not re-fetch them", got.ContentHash)
	}
	if got.Size != 4 || got.Mtime != 1234 || got.DeviceID != "dev-1" {
		t.Errorf("the entry lost attributes across the rename: %+v", got)
	}
}

// A renamed directory moves every path beneath it, and the entries have to
// follow — including ones whose own names were already portable.
func TestTheManifestFollowsARenamedDirectory(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Etc./01 - Track.flac", "one")
	manifest := newFakeManifest(db.FileManifest{
		CanonicalID: "dev-1:Etc./01 - Track.flac",
		ContentHash: "hash-one",
		RelPath:     "Etc./01 - Track.flac",
		DeviceID:    "dev-1",
	})

	if _, err := New(root, nil).WithManifest(manifest, "dev-1").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, _ := manifest.ListFileManifests(context.Background())
	if len(rows) != 1 || rows[0].RelPath != "Etc/01 - Track.flac" {
		t.Fatalf("the entry did not follow its directory: %+v", rows)
	}
	if rows[0].CanonicalID != "dev-1:Etc/01 - Track.flac" {
		t.Fatalf("canonical id = %q, want it to address the new path", rows[0].CanonicalID)
	}
}

// A peer's entries are not this device's to move: the file lives on that
// device, under whatever name that device gave it.
func TestAPeersManifestEntriesAreLeftAlone(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Pixies/Where Is My Mind?.flac", "mind")
	manifest := newFakeManifest(
		db.FileManifest{CanonicalID: "dev-1:Pixies/Where Is My Mind?.flac", ContentHash: "h", RelPath: "Pixies/Where Is My Mind?.flac", DeviceID: "dev-1"},
		db.FileManifest{CanonicalID: "dev-2:Elsewhere/Other.flac", ContentHash: "h2", RelPath: "Elsewhere/Other.flac", DeviceID: "dev-2"},
	)
	if _, err := New(root, nil).WithManifest(manifest, "dev-1").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.rows["dev-2:Elsewhere/Other.flac"]; !ok {
		t.Fatal("a peer's manifest entry was rewritten by this device's migration")
	}
}

// The migration can be killed between renaming the files and updating the
// manifest. Rerunning has to finish the job — and it cannot do that from the
// rename list, because a rerun finds every name already portable and has
// nothing to replay. The repair is derived from what is on disk instead.
func TestARerunRepairsAManifestLeftBehindByACrash(t *testing.T) {
	root := t.TempDir()
	// The state a crash leaves: the file is already at its new name, the
	// manifest still describes the old one.
	write(t, root, "Pixies/Where Is My Mind_.flac", "mind")
	manifest := newFakeManifest(db.FileManifest{
		CanonicalID: "dev-1:Pixies/Where Is My Mind?.flac",
		ContentHash: "the-hash",
		Size:        4,
		RelPath:     "Pixies/Where Is My Mind?.flac",
		Mtime:       1234,
		DeviceID:    "dev-1",
	})

	res, err := New(root, nil).WithManifest(manifest, "dev-1").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Renamed) != 0 {
		t.Fatalf("the files were already migrated; nothing should move: %v", res.Renamed)
	}

	rows, _ := manifest.ListFileManifests(context.Background())
	if len(rows) != 1 {
		t.Fatalf("want exactly one entry, got %+v", rows)
	}
	if rows[0].RelPath != "Pixies/Where Is My Mind_.flac" {
		t.Fatalf("the manifest still points at a file that is not there: %q", rows[0].RelPath)
	}
	if rows[0].ContentHash != "the-hash" {
		t.Fatalf("the repair changed the content hash to %q; peers would re-fetch the bytes", rows[0].ContentHash)
	}
}

// A file that went somewhere the deterministic mapping does not predict — a
// collision resolved to "… (2)", or one the owner moved themselves — is left to
// the library scan, which finds it by re-hashing. Guessing here is how a
// manifest comes to point at the wrong bytes.
func TestTheRepairDoesNotGuessWhereAFileWent(t *testing.T) {
	root := t.TempDir()
	write(t, root, "A/Title (2).flac", "moved somewhere unpredictable")
	manifest := newFakeManifest(db.FileManifest{
		CanonicalID: "dev-1:A/Title?.flac",
		ContentHash: "h",
		RelPath:     "A/Title?.flac",
		DeviceID:    "dev-1",
	})
	if _, err := New(root, nil).WithManifest(manifest, "dev-1").Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := manifest.rows["dev-1:A/Title?.flac"]; !ok {
		t.Fatal("the entry was rewritten to a path the migration cannot know it went to")
	}
}

// The device that holds the unportable names is the one that must migrate, and
// it is not the device that notices the problem — a Linux box can store every
// one of these names quite happily. So the offer has to be driven by what this
// device holds, not by what some peer failed to pull.
func TestPendingCountsThisDevicesOwnUnportableNames(t *testing.T) {
	manifest := newFakeManifest(
		db.FileManifest{CanonicalID: "dev-1:A/Where Is My Mind?.flac", RelPath: "A/Where Is My Mind?.flac", DeviceID: "dev-1"},
		db.FileManifest{CanonicalID: "dev-1:A/Etc./Track.flac", RelPath: "A/Etc./Track.flac", DeviceID: "dev-1"},
		db.FileManifest{CanonicalID: "dev-1:A/Fine.flac", RelPath: "A/Fine.flac", DeviceID: "dev-1"},
		// A peer's file is that peer's to rename.
		db.FileManifest{CanonicalID: "dev-2:B/Also Bad?.flac", RelPath: "B/Also Bad?.flac", DeviceID: "dev-2"},
	)
	got, err := New(t.TempDir(), nil).WithManifest(manifest, "dev-1").Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("Pending = %d, want 2", got)
	}
}

func TestPendingIsZeroForAMigratedLibrary(t *testing.T) {
	manifest := newFakeManifest(
		db.FileManifest{CanonicalID: "dev-1:A/Fine.flac", RelPath: "A/Fine.flac", DeviceID: "dev-1"},
	)
	got, err := New(t.TempDir(), nil).WithManifest(manifest, "dev-1").Pending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("Pending = %d, want 0", got)
	}
}
