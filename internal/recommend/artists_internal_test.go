package recommend

import (
	"slices"
	"testing"
)

// Sources name the same artist differently and only some of them carry an
// MBID. Once a merge learns an MBID for an artist it already holds, a later
// candidate with that MBID under another name has to join it rather than
// become a second card for the same artist.
func TestMergeArtistCandidatesIndexesAnMBIDLearnedDuringTheMerge(t *testing.T) {
	got := mergeArtistCandidates([]ArtistCandidate{
		{Name: "Björk", Sources: []string{"deezer"}},
		{Name: "Björk", MBID: "bjork-mbid", Sources: []string{"listenbrainz"}},
		{Name: "Björk Guðmundsdóttir", MBID: "bjork-mbid", Sources: []string{"lastfm"}},
	}, ArtistSeed{Name: "Seed Artist"})

	if len(got) != 1 {
		t.Fatalf("got %d artists, want 1: %+v", len(got), got)
	}
	if got[0].MBID != "bjork-mbid" {
		t.Fatalf("merged artist MBID = %q, want bjork-mbid", got[0].MBID)
	}
	if want := []string{"deezer", "listenbrainz", "lastfm"}; !slices.Equal(got[0].Sources, want) {
		t.Fatalf("sources = %v, want %v", got[0].Sources, want)
	}
}
