//go:build !windows

package portablename

import "testing"

// A colon or a question mark is perfectly writable here. Refusing a file this
// device can hold, because some other platform could not, would stop it
// replicating for no reason — the household's answer to that name is the
// migration, not a permanent refusal.
func TestLocallyStorableAcceptsNamesOnlyWindowsRefuses(t *testing.T) {
	for _, rel := range []string{
		"Artist/Where Is My Mind?.flac",
		"Artist/Symphony No. 5: Allegro.flac",
		"Artist/NUL.mp3",
		"Artist/Etc..",
	} {
		if !LocallyStorable(rel) {
			t.Errorf("LocallyStorable(%q) = false, but this filesystem can write it", rel)
		}
	}
}
