package portablename

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every character Windows refuses has to leave the name, whichever platform
// minted it. These are the cases that reach a download template from real tag
// values: a colon in a subtitle, a question mark in a title, quotes around a
// nickname.
func TestSegmentReplacesIllegalCharacters(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"colon", "Symphony No. 5: Allegro", "Symphony No. 5- Allegro"},
		{"question mark", "Where Is My Mind?", "Where Is My Mind_"},
		{"asterisk", "F*ck It", "F+ck It"},
		{"quotes", `The "Real" Thing`, "The 'Real' Thing"},
		{"angle brackets", "<Intro>", "(Intro)"},
		{"pipe", "A|B", "A-B"},
		{"backslash", `AC\DC`, "AC-DC"},
		{"forward slash", "AC/DC", "AC-DC"},
		{"control character", "Hello\x07World", "Hello_World"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Segment(tc.in)
			if got != tc.want {
				t.Fatalf("Segment(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !IsPortable(got) {
				t.Fatalf("Segment(%q) = %q, which is still not portable", tc.in, got)
			}
		})
	}
}

// Windows rejects more than the illegal character set, and these two classes
// are the ones a Linux-minted name reaches a Windows peer carrying.
func TestSegmentHandlesTrailingAndReservedNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"trailing dot", "Etc.", "Etc"},
		{"trailing space", "Fade Out ", "Fade Out"},
		{"trailing dot and space", "Mix. ", "Mix"},
		{"reserved bare", "CON", "CON_"},
		{"reserved lowercase", "nul", "nul_"},
		{"reserved serial port", "COM1", "COM1_"},
		{"reserved printer port", "LPT9", "LPT9_"},
		{"not reserved", "CONCERT", "CONCERT"},
		{"sanitises to nothing", "...", "_"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Segment(tc.in); got != tc.want {
				t.Fatalf("Segment(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A reserved stem is reserved whatever extension follows it, and the extension
// has to survive sanitisation or the file stops being playable.
func TestSegmentKeepsTheExtension(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"reserved with extension", "NUL.mp3", "NUL_.mp3"},
		{"illegal character", "Where Is My Mind?.flac", "Where Is My Mind_.flac"},
		{"trailing dot before extension", "Etc..flac", "Etc..flac"},
		{"trailing space after extension", "Song.flac ", "Song.flac"},
		{"no extension", "Song", "Song"},
		{"dotfile is a stem", ".hidden", ".hidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Segment(tc.in); got != tc.want {
				t.Fatalf("Segment(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Sanitisation is per component: the separators between directories are the one
// place a slash is meaningful, and a directory called "Etc." is as unstorable
// as a file called "Etc.".
func TestRelPathSanitisesEachComponent(t *testing.T) {
	got := RelPath("AC/DC/Back In Black: Remastered/01 - Hells Bells?.flac")
	want := "AC/DC/Back In Black- Remastered/01 - Hells Bells_.flac"
	if got != want {
		t.Fatalf("RelPath = %q, want %q", got, want)
	}
	if !IsPortable(got) {
		t.Fatalf("RelPath produced a path that is still not portable: %q", got)
	}
}

func TestIsPortableAcceptsAnAlreadyCleanPath(t *testing.T) {
	clean := "Artist/Album/01 - Track.flac"
	if !IsPortable(clean) {
		t.Fatalf("%q should already be portable", clean)
	}
	if RelPath(clean) != clean {
		t.Fatalf("RelPath changed an already-portable path: %q", RelPath(clean))
	}
}

// The replacement alphabet is smaller than the input alphabet, so two titles
// can sanitise onto one name. Unique is what keeps the second one from landing
// on the first.
func TestUniqueAvoidsAnOccupiedName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Song.flac"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Unique(dir, "Song.flac")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Song (2).flac" {
		t.Fatalf("Unique = %q, want %q", got, "Song (2).flac")
	}
	if err := os.WriteFile(filepath.Join(dir, got), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Unique(dir, "Song.flac")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Song (3).flac" {
		t.Fatalf("Unique = %q, want %q", got, "Song (3).flac")
	}
	// The occupant is untouched: a collision must not cost either file.
	first, err := os.ReadFile(filepath.Join(dir, "Song.flac"))
	if err != nil || string(first) != "first" {
		t.Fatalf("original file was disturbed: %q %v", first, err)
	}
}

func TestUniqueLeavesAFreeNameAlone(t *testing.T) {
	dir := t.TempDir()
	got, err := Unique(dir, "Song.flac")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Song.flac" {
		t.Fatalf("Unique = %q, want the name unchanged", got)
	}
}

// Sanitisation preserves the distinctions that survive the alphabet: two
// titles differing in an illegal character must not become one name unless
// they differ only in characters that share a replacement.
func TestDistinctTitlesStayDistinct(t *testing.T) {
	a := Segment("Where Is My Mind?")
	b := Segment("Where Is My Mind")
	if a == b {
		t.Fatalf("distinct titles collapsed onto %q", a)
	}
	if !strings.Contains(a, "Where Is My Mind") {
		t.Fatalf("title is no longer recognisable: %q", a)
	}
}
