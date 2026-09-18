package portablename

import (
	"os"
	"path/filepath"
	"testing"
)

// write lays down a fixture, skipping the test when this filesystem cannot
// hold the name.
//
// Most of these cases are non-portable names sitting on disk, which is a state
// only a filesystem permissive enough to create them can reach. Windows is the
// platform the portable rules are drawn from, so it refuses those names
// outright -- or worse, accepts the write and silently drops a trailing dot,
// leaving a fixture that is not what the test asked for. Either way there is no
// sweep to test there, and LocallyStorable is already the per-OS answer to
// whether this device can hold a name.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if !LocallyStorable(name) {
		t.Skipf("this filesystem cannot store %q, so the case cannot arise here", name)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The adapter that substitutes its own placeholders can still mint a name
// Windows refuses. The sweep is what catches it before the library scan does.
func TestSweepNewRenamesAFileTheTemplateCouldNotSanitise(t *testing.T) {
	dir := t.TempDir()
	before, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "Pixies - Where Is My Mind_.flac", "audio")
	// spotDL strips the illegal characters itself but leaves a trailing dot and
	// a device name behind.
	write(t, dir, "Nine Inch Nails - Closer.", "audio")
	write(t, dir, "NUL.mp3", "audio")

	moved, err := SweepNew(dir, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 {
		t.Fatalf("expected 2 renames, got %v", moved)
	}
	for _, name := range []string{"Nine Inch Nails - Closer", "NUL_.mp3", "Pixies - Where Is My Mind_.flac"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %q to exist: %v", name, err)
		}
	}
}

// A library that was already on disk is not this download's business; a sweep
// that renamed it would be a migration running behind the owner's back.
func TestSweepNewLeavesExistingFilesAlone(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Legacy - Track.", "old")
	before, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "New - Track.", "new")

	moved, err := SweepNew(dir, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].To != "New - Track" {
		t.Fatalf("expected only the new file renamed, got %v", moved)
	}
	if _, err := os.Stat(filepath.Join(dir, "Legacy - Track.")); err != nil {
		t.Fatalf("the pre-existing file was renamed: %v", err)
	}
}

// Two titles that sanitise onto one name must both survive.
func TestSweepNewResolvesACollision(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Artist - Title.flac", "first")
	before, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, "Artist - Title?.flac", "second")
	// "?" maps onto "_", so this one also has to move out of the way.
	write(t, dir, "Artist - Title_.flac", "third")

	if _, err := SweepNew(dir, before); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "Artist - Title.flac"))
	if err != nil || string(first) != "first" {
		t.Fatalf("the occupant was lost: %q %v", first, err)
	}
	bodies := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		bodies[string(b)] = true
		if !IsPortable(e.Name()) {
			t.Fatalf("%q is still not portable", e.Name())
		}
	}
	for _, want := range []string{"first", "second", "third"} {
		if !bodies[want] {
			t.Fatalf("%q was lost; directory holds %v", want, bodies)
		}
	}
}

func TestSnapshotOfAMissingDirectoryIsEmpty(t *testing.T) {
	got, err := Snapshot(filepath.Join(t.TempDir(), "not-yet"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty snapshot, got %v", got)
	}
}

// Unique picks a free name, but something else can take it before the move —
// a download landing while a migration runs. The move has to refuse rather
// than replace.
func TestMoveWithoutClobberingRefusesAnOccupiedDestination(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "from.flac", "moving")
	write(t, dir, "to.flac", "occupant")

	if err := MoveWithoutClobbering(dir, "from.flac", "to.flac", false); err == nil {
		t.Fatal("moving onto an occupied name must fail rather than replace it")
	}
	occupant, err := os.ReadFile(filepath.Join(dir, "to.flac"))
	if err != nil || string(occupant) != "occupant" {
		t.Fatalf("the occupant was replaced: %q %v", occupant, err)
	}
	moving, err := os.ReadFile(filepath.Join(dir, "from.flac"))
	if err != nil || string(moving) != "moving" {
		t.Fatalf("the file being moved was lost: %q %v", moving, err)
	}
}

func TestMoveWithoutClobberingMovesToAFreeName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "from.flac", "moving")
	if err := MoveWithoutClobbering(dir, "from.flac", "to.flac", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "from.flac")); !os.IsNotExist(err) {
		t.Fatalf("the original name survived the move: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "to.flac"))
	if err != nil || string(got) != "moving" {
		t.Fatalf("the file did not arrive intact: %q %v", got, err)
	}
}

func TestMoveWithoutClobberingMovesADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "old", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MoveWithoutClobbering(dir, "old", "new", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "inner")); err != nil {
		t.Fatalf("the directory did not move with its contents: %v", err)
	}
}

// Downloads run concurrently into one shared output directory, so "new since
// the snapshot" can also mean another job's file. A finished one is harmless to
// rename, but a partial one must not be touched: the job still writing it would
// fail, and the owner would lose that track.
func TestSweepNewLeavesAnotherDownloadsPartialFileAlone(t *testing.T) {
	dir := t.TempDir()
	before, err := Snapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A concurrent yt-dlp job mid-download, and its post-processor's scratch
	// file. Both carry names that are not portable.
	write(t, dir, "Other Job - Title?.opus.part", "partial")
	write(t, dir, "Other Job - Title?.temp", "post-processing")
	write(t, dir, ".Other Job - Title?.hidden", "scratch")
	// And this job's finished file.
	write(t, dir, "This Job - Title?.opus", "done")

	moved, err := SweepNew(dir, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 || moved[0].From != "This Job - Title?.opus" {
		t.Fatalf("expected only the finished file renamed, got %v", moved)
	}
	for _, name := range []string{
		"Other Job - Title?.opus.part",
		"Other Job - Title?.temp",
		".Other Job - Title?.hidden",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("an in-progress file was renamed out from under the job writing it: %q: %v", name, err)
		}
	}
}
