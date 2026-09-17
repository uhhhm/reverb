package p2p

import "testing"

// A drive-relative path is Windows's own escape hatch. "C:evil" is not absolute
// by filepath.IsAbs and survives filepath.Clean unchanged, but Windows resolves
// it against that drive's current directory rather than under the music
// directory — so a peer that chooses this string as a rel path reaches outside
// the library on a Windows device and nowhere at all on a Unix one.
//
// This test is Windows-only because the behaviour it guards is: on Unix
// "C:evil" is an ordinary file name with a colon in it, and refusing it there
// would be wrong. The same is true of a UNC path.
func TestValidateRelPathRejectsDriveRelativeAndUNCPaths(t *testing.T) {
	for _, bad := range []string{
		`C:evil`,
		`C:\Windows\System32\config\SAM`,
		`C:..\..\secrets.txt`,
		`\\server\share\secrets.txt`,
	} {
		if got, err := validateRelPath(bad); err == nil {
			t.Errorf("validateRelPath(%q) = %q, want an error: it does not resolve under the music directory", bad, got)
		}
	}
	// The ordinary case still has to pass, or the check has simply broken
	// fetching on Windows.
	if got, err := validateRelPath("Artist/Album/01.flac"); err != nil || got == "" {
		t.Fatalf("valid path rejected on Windows: %q %v", got, err)
	}
}
