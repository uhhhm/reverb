package recommend_test

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/store"
)

// A Radio session records every queued track as a play, and one the owner
// skipped is stored unqualified. Only tracks both heard in one session count as
// listened to together. None of the tracks below shares an artist, album or
// playlist with the seed, so any score comes from session co-occurrence alone.
//
//	radio-1:  seed heard,   "Skipped" skipped, "Also skipped" skipped
//	radio-2:  seed skipped, "Heard after skip" heard
//	evening:  seed heard,   "Heard together" heard
func seedCoOccurrenceStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/local.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, row := range []struct{ id, title, artist, album string }{
		{"seed", "Seed", "Alpha", "First"},
		{"skipped", "Skipped", "Beta", "Second"},
		{"also-skipped", "Also skipped", "Gamma", "Third"},
		{"after-skip", "Heard after skip", "Delta", "Fourth"},
		{"together", "Heard together", "Epsilon", "Fifth"},
	} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO catalog_entity(id,kind,title,artist,album,created_at) VALUES(?, 'track', ?, ?, ?, 1)`, row.id, row.title, row.artist, row.album); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO backend_binding(catalog_id,library_identity,backend_id,binding_epoch,resolved_at) VALUES(?, 'library', ?, 1, 1)`, row.id, "backend-"+row.id); err != nil {
			t.Fatal(err)
		}
	}
	for i, p := range []struct {
		catalogID, session string
		qualified          int
	}{
		{"seed", "radio-1", 1},
		{"skipped", "radio-1", 0},
		{"also-skipped", "radio-1", 0},
		{"seed", "radio-2", 0},
		{"after-skip", "radio-2", 1},
		{"seed", "evening", 1},
		{"together", "evening", 1},
	} {
		at := int64(100 + i)
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO plays(id,user_id,catalog_id,played_at,ms_played,completed,created_at,session_id,qualified) VALUES(?, 'local', ?, ?, 1, ?, ?, ?, ?)`,
			"play-"+p.catalogID+"-"+p.session, p.catalogID, at, p.qualified, at, p.session, p.qualified); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestSkippedPlaysDoNotCoOccurInLocalTracks(t *testing.T) {
	st := seedCoOccurrenceStore(t)
	local := recommend.NewLocalSimilarity(st.Q(), func() recommend.LocalLibrary { return localLibrary{} })
	tracks, err := local.SimilarLocalTracks(context.Background(), recommend.TrackSeed{Artist: "Alpha", Title: "Seed"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(tracks); !reflect.DeepEqual(got, []string{"Heard together"}) {
		t.Fatalf("similar tracks = %v, want only the track heard with the seed", got)
	}
}

func TestSkippedPlaysDoNotCoOccurInLocalArtists(t *testing.T) {
	st := seedCoOccurrenceStore(t)
	local := recommend.NewLocalSimilarity(st.Q(), func() recommend.LocalLibrary { return localLibrary{} })
	artists, err := local.SimilarLocalArtists(context.Background(), recommend.ArtistSeed{Name: "Alpha"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range artists {
		names = append(names, a.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Epsilon"}) {
		t.Fatalf("similar artists = %v, want only the artist heard with the seed", names)
	}
}

func TestSkippedPlaysDoNotCoOccurInPlayableTracks(t *testing.T) {
	st := seedCoOccurrenceStore(t)
	lib := playableLibrary{tracks: []core.Track{
		{ID: "file-seed", Title: "Seed", Artist: "Alpha", Album: "First"},
		{ID: "file-skipped", Title: "Skipped", Artist: "Beta", Album: "Second"},
		{ID: "file-also-skipped", Title: "Also skipped", Artist: "Gamma", Album: "Third"},
		{ID: "file-after-skip", Title: "Heard after skip", Artist: "Delta", Album: "Fourth"},
		{ID: "file-together", Title: "Heard together", Artist: "Epsilon", Album: "Fifth"},
	}}
	local := recommend.NewPlayableSimilarity(st.Q(), func() recommend.PlayableLibrary { return lib })
	tracks, err := local.SimilarLocalTracks(context.Background(), recommend.TrackSeed{Artist: "Alpha", Title: "Seed"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(tracks); !reflect.DeepEqual(got, []string{"Heard together"}) {
		t.Fatalf("playable similar tracks = %v, want only the track heard with the seed", got)
	}
}
