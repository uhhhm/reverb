package portablemigrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/uhhhm/reverb/internal/portablename"
)

// write lays down a fixture, skipping the test when this filesystem cannot
// hold the path.
//
// The migration exists for a library that already holds names another platform
// cannot store, which is a state only the permissive platform can reach.
// Windows refuses those names outright -- or accepts the write having silently
// dropped a trailing dot, leaving a fixture that is not what the test asked
// for. There is nothing for the migration to do on a device that could never
// have accumulated such a library, and LocallyStorable is already the per-OS
// answer to whether this device can hold a path.
func write(t *testing.T, root, rel, body string) {
	t.Helper()
	if !portablename.LocallyStorable(rel) {
		t.Skipf("this filesystem cannot store %q, so the case cannot arise here", rel)
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// everything lists the library as slash-relative paths with their contents, so
// a test can assert on the whole shape rather than file by file.
func everything(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// An owner who has been running Reverb on Linux already has tracks whose names
// Windows cannot store. They are what keeps a newly paired Windows device from
// ever completing its first sync.
func TestRenamesTheNamesAnotherPlatformCannotStore(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Pixies/Where Is My Mind?.flac", "mind")
	write(t, root, "AC-DC/Back In Black: Remastered/01 - Hells Bells.flac", "bells")
	write(t, root, "Weezer/Etc./Track.flac", "etc")
	write(t, root, "Oddities/NUL.mp3", "nul")
	// Already portable: it must not be touched.
	write(t, root, "Radiohead/OK Computer/01 - Airbag.flac", "airbag")

	res, err := rename(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %+v", res.Failed)
	}

	got := everything(t, root)
	want := map[string]string{
		"Pixies/Where Is My Mind_.flac":                         "mind",
		"AC-DC/Back In Black- Remastered/01 - Hells Bells.flac": "bells",
		"Weezer/Etc/Track.flac":                                 "etc",
		"Oddities/NUL_.mp3":                                     "nul",
		"Radiohead/OK Computer/01 - Airbag.flac":                "airbag",
	}
	if len(got) != len(want) {
		t.Fatalf("library holds %v, want %v", got, want)
	}
	for path, body := range want {
		if got[path] != body {
			t.Errorf("%q = %q, want %q", path, got[path], body)
		}
	}
	for path := range got {
		if !portablename.IsPortable(path) {
			t.Errorf("%q is still not storable on every platform", path)
		}
	}
}

// A directory's contents have to move while the directory still has the name
// they were found under, or the second rename is against a path that no longer
// exists.
func TestRenamesDeepestFirst(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Etc./Sub?/Track?.flac", "deep")

	res, err := rename(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %+v", res.Failed)
	}
	if _, err := os.Stat(filepath.Join(root, "Etc", "Sub_", "Track_.flac")); err != nil {
		t.Fatalf("the nested track did not arrive at a portable path: %v", err)
	}
	var order []string
	for _, r := range res.Renamed {
		order = append(order, r.From)
	}
	if len(order) != 3 {
		t.Fatalf("want three renames, got %v", order)
	}
	if order[0] != "Etc./Sub?/Track?.flac" {
		t.Fatalf("the file must move before its directories, got %v", order)
	}
}

// A rename that would collide with an existing file must not destroy either
// one. This is the case where two titles differing only in an illegal character
// sanitise onto the same name.
func TestACollisionCostsNeitherFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "A/Title_.flac", "already here")
	write(t, root, "A/Title?.flac", "migrating")

	if _, err := rename(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	got := everything(t, root)
	var bodies []string
	for path, body := range got {
		bodies = append(bodies, body)
		if !portablename.IsPortable(path) {
			t.Errorf("%q is still not portable", path)
		}
	}
	sort.Strings(bodies)
	if len(bodies) != 2 || bodies[0] != "already here" || bodies[1] != "migrating" {
		t.Fatalf("both files must survive a collision; library holds %v", got)
	}
	if got["A/Title_.flac"] != "already here" {
		t.Fatalf("the occupant was overwritten: %v", got)
	}
}

// A crash, a force quit or a closed lid leaves a partial migration. Every file
// must be at either its old name or its new one, with nothing half-renamed, and
// rerunning must finish the job.
func TestAnInterruptedMigrationIsResumable(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"A/1?.flac", "A/2?.flac", "A/3?.flac", "A/4?.flac"} {
		write(t, root, rel, rel)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the harshest interruption: stopped before a single rename

	res, err := rename(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Renamed) != 0 {
		t.Fatalf("a cancelled run should have renamed nothing, got %v", res.Renamed)
	}
	// Every file is still at its old name, and nothing was lost.
	if len(everything(t, root)) != 4 {
		t.Fatalf("files went missing: %v", everything(t, root))
	}

	// Rerunning finishes the job, deriving the work from what is on disk.
	if _, err := rename(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	got := everything(t, root)
	if len(got) != 4 {
		t.Fatalf("files went missing on the second run: %v", got)
	}
	for path, body := range got {
		if !portablename.IsPortable(path) {
			t.Errorf("%q is still not portable", path)
		}
		if body == "" {
			t.Errorf("%q lost its contents", path)
		}
	}
}

// A second run over an already-migrated library must do nothing at all, or the
// names would drift a little further every time.
func TestRerunningAMigratedLibraryChangesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Pixies/Where Is My Mind?.flac", "mind")
	if _, err := rename(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	first := everything(t, root)
	res, err := rename(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Renamed) != 0 {
		t.Fatalf("a migrated library should need no further renames, got %v", res.Renamed)
	}
	second := everything(t, root)
	if len(first) != len(second) {
		t.Fatalf("the library changed on a second run: %v then %v", first, second)
	}
	for path, body := range first {
		if second[path] != body {
			t.Fatalf("%q changed on a second run", path)
		}
	}
}

// Hidden entries are not the owner's music, and a leading dot is legal
// everywhere anyway.
func TestHiddenEntriesAreLeftAlone(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".cache/scratch?.tmp", "scratch")
	if _, err := rename(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".cache", "scratch?.tmp")); err != nil {
		t.Fatalf("a hidden file was migrated: %v", err)
	}
}

// The library has to end up agreeing with what is on disk, so reconciliation is
// part of a run rather than something the caller must remember.
func TestRunReconcilesTheLibrary(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Pixies/Where Is My Mind?.flac", "mind")
	reconciled := false
	res, err := New(root, func(context.Context) error { reconciled = true; return nil }).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Renamed) != 1 {
		t.Fatalf("want one rename, got %v", res.Renamed)
	}
	if !reconciled {
		t.Fatal("the library was left pointing at paths that no longer exist")
	}
}

// Files that did move are already on disk under their new names. A library
// still pointing at the old ones would serve tracks that are not there, so a
// reconciliation failure is reported without pretending the renames did not
// happen.
func TestAReconcileFailureStillReportsTheRenames(t *testing.T) {
	root := t.TempDir()
	write(t, root, "Pixies/Where Is My Mind?.flac", "mind")
	boom := errors.New("library unavailable")
	res, err := New(root, func(context.Context) error { return boom }).Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
	if len(res.Renamed) != 1 {
		t.Fatalf("the renames that succeeded must still be reported, got %v", res.Renamed)
	}
}

func TestRunNeedsAMusicDirectory(t *testing.T) {
	if _, err := New("", nil).Run(context.Background()); err == nil {
		t.Fatal("a migration with nowhere to run must say so")
	}
}

// A move links the new name and then unlinks the old one. Killed in between,
// it leaves the same bytes under both names. A rerun must finish that move, not
// treat the leftover as a third track and mint "Name (2)" for it.
func TestARerunFinishesAHalfCompletedMove(t *testing.T) {
	root := t.TempDir()
	write(t, root, "A/Title?.flac", "the track")
	// Exactly the state an interruption leaves: both names, one inode.
	if err := os.Link(filepath.Join(root, "A", "Title?.flac"), filepath.Join(root, "A", "Title_.flac")); err != nil {
		t.Skipf("this filesystem cannot hard-link, so it cannot reach the state under test: %v", err)
	}

	res, err := rename(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %+v", res.Failed)
	}
	got := everything(t, root)
	want := map[string]string{"A/Title_.flac": "the track"}
	if len(got) != len(want) {
		t.Fatalf("library holds %v, want exactly %v — the leftover became another copy", got, want)
	}
	if got["A/Title_.flac"] != "the track" {
		t.Fatalf("the track did not survive: %v", got)
	}
	if len(res.Renamed) != 1 || res.Renamed[0].To != "A/Title_.flac" {
		t.Fatalf("the completed move should be reported, got %v", res.Renamed)
	}
}

// Two genuinely different tracks that sanitise onto one name are not an
// interrupted move, and must both survive.
func TestALeftoverIsNotConfusedWithARealCollision(t *testing.T) {
	root := t.TempDir()
	write(t, root, "A/Title_.flac", "a different track")
	write(t, root, "A/Title?.flac", "the migrating track")

	if _, err := rename(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	got := everything(t, root)
	if len(got) != 2 {
		t.Fatalf("both tracks must survive, library holds %v", got)
	}
	if got["A/Title_.flac"] != "a different track" {
		t.Fatalf("the occupant was replaced: %v", got)
	}
}
