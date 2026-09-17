package portablename

import "testing"

// On Windows the portable rules are the local rules, so a peer's legacy name is
// recognisable as unwritable before any network work happens.
func TestLocallyStorableRefusesWhatWindowsRefuses(t *testing.T) {
	for _, rel := range []string{
		"Artist/Where Is My Mind?.flac",
		"Artist/Symphony No. 5: Allegro.flac",
		"Artist/Etc..",
		"Artist/NUL.mp3",
		"Etc. /Track.flac",
	} {
		if LocallyStorable(rel) {
			t.Errorf("LocallyStorable(%q) = true, but Windows cannot write it", rel)
		}
	}
	if !LocallyStorable("Artist/Album/01 - Track.flac") {
		t.Error("an ordinary path must stay fetchable")
	}
}
