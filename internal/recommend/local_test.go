package recommend_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/store"
)

type localLibrary struct{}

func (localLibrary) Search(_ context.Context, query string, _ []core.EntityType) (core.SearchResults, error) {
	return core.SearchResults{Artists: []core.Artist{{ID: "artist-" + query, Name: query}}}, nil
}

func TestLocalSimilarityUsesAlbumPlaylistAndPlaybackSessionSignals(t *testing.T) {
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
		{"seed", "Seed", "Alpha", "Shared"},
		{"album", "Album peer", "Beta", "Shared"},
		{"playlist", "Playlist peer", "Gamma", "Elsewhere"},
		{"session", "Session peer", "Delta", "Elsewhere"},
		{"unrelated", "Unrelated", "Epsilon", "Elsewhere"},
	} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO catalog_entity(id,kind,title,artist,album,created_at) VALUES(?, 'track', ?, ?, ?, 1)`, row.id, row.title, row.artist, row.album); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO backend_binding(catalog_id,library_identity,backend_id,binding_epoch,resolved_at) VALUES(?, 'library', ?, 1, 1)`, row.id, "backend-"+row.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO synced_playlists(id,source,external_id,name,tracks_json,mode,created_at) VALUES('p','library','p','P','[{"title":"Seed","artist":"Alpha"},{"title":"Playlist peer","artist":"Gamma"}]','once',1)`); err != nil {
		t.Fatal(err)
	}
	for id, at := range map[string]int64{"seed": 100, "session": 101} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO plays(id,user_id,catalog_id,played_at,ms_played,completed,created_at,session_id) VALUES(?, 'local', ?, ?, 1, 0, ?, 'session-1')`, "play-"+id, id, at, at); err != nil {
			t.Fatal(err)
		}
	}

	local := recommend.NewLocalSimilarity(st.Q(), func() recommend.LocalLibrary { return localLibrary{} })
	tracks, err := local.SimilarLocalTracks(ctx, recommend.TrackSeed{Artist: "Alpha", Title: "Seed"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(tracks); !reflect.DeepEqual(got, []string{"Session peer", "Playlist peer", "Album peer"}) {
		t.Fatalf("local tracks = %v", got)
	}
	for _, track := range tracks {
		if track.Source != "library" || track.Match == nil || track.Match.Status != core.MatchInLibrary {
			t.Fatalf("not locally playable: %+v", track)
		}
	}

	artists, err := local.SimilarLocalArtists(ctx, recommend.ArtistSeed{Name: "Alpha"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(artists) != 2 || artists[0].Source != "library" {
		t.Fatalf("local artists = %+v", artists)
	}
}

// playableLibrary is a phone's folder library: it can play its tracks, and
// nothing binds them to catalog entities.
type playableLibrary struct {
	localLibrary
	tracks []core.Track
}

func (l playableLibrary) GetSongsBrowse(_ context.Context, size, offset int) ([]core.Track, error) {
	if offset >= len(l.tracks) {
		return nil, nil
	}
	out := l.tracks[offset:]
	if size > 0 && len(out) > size {
		out = out[:size]
	}
	return out, nil
}

// On a phone, similarity scores the tracks its library can play, which have
// no binding: a catalog entity bound to nothing on this device, however
// similar, is never offered, and a playable track keeps its backend id and,
// where the household catalogue knows it, its catalog id.
func TestPlayableSimilarityScoresTheLibrarysOwnTracks(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/local.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Catalog entities replicated from a desktop; none is bound here.
	for _, row := range []struct{ id, title, artist, album string }{
		{"cat-seed", "Seed", "Alpha", "Shared"},
		{"cat-session", "Session peer", "Delta", "Elsewhere"},
		{"cat-remote", "Remote twin", "Alpha", "Shared"},
	} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO catalog_entity(id,kind,title,artist,album,created_at) VALUES(?, 'track', ?, ?, ?, 1)`, row.id, row.title, row.artist, row.album); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO synced_playlists(id,source,external_id,name,tracks_json,mode,created_at) VALUES('p','library','p','P','[{"title":"Seed","artist":"Alpha"},{"title":"Playlist peer","artist":"Gamma"}]','once',1)`); err != nil {
		t.Fatal(err)
	}
	for id, at := range map[string]int64{"cat-seed": 100, "cat-session": 101} {
		if _, err := st.DB().ExecContext(ctx, `INSERT INTO plays(id,user_id,catalog_id,played_at,ms_played,completed,created_at,session_id) VALUES(?, 'local', ?, ?, 1, 0, ?, 'session-1')`, "play-"+id, id, at, at); err != nil {
			t.Fatal(err)
		}
	}
	lib := playableLibrary{tracks: []core.Track{
		{ID: "file-seed", Title: "Seed", Artist: "Alpha", Album: "Shared"},
		{ID: "file-album", Title: "Album peer", Artist: "Beta", Album: "shared", CoverArtID: "cover-album", DurationMs: 1000},
		{ID: "file-playlist", Title: "Playlist peer", Artist: "gamma", Album: "Elsewhere"},
		{ID: "file-session", Title: "Session peer", Artist: "Delta", Album: "Elsewhere"},
		{ID: "file-unrelated", Title: "Unrelated", Artist: "Epsilon", Album: "Elsewhere"},
	}}

	local := recommend.NewPlayableSimilarity(st.Q(), func() recommend.PlayableLibrary { return lib })
	tracks, err := local.SimilarLocalTracks(ctx, recommend.TrackSeed{Artist: "alpha ", Title: "seed"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(tracks); !reflect.DeepEqual(got, []string{"Session peer", "Playlist peer", "Album peer"}) {
		t.Fatalf("playable tracks = %v", got)
	}
	byTitle := map[string]core.ExternalResult{}
	for _, track := range tracks {
		byTitle[track.Title] = track
		if track.Source != "library" || track.Match == nil || track.Match.Status != core.MatchInLibrary || track.Match.LibraryTrackID != track.ExternalID {
			t.Fatalf("not locally playable: %+v", track)
		}
	}
	if got := byTitle["Session peer"]; got.ExternalID != "file-session" || got.CanonicalID != "cat-session" {
		t.Fatalf("session peer ids = %q, %q", got.ExternalID, got.CanonicalID)
	}
	if got := byTitle["Album peer"]; got.ExternalID != "file-album" || got.CanonicalID != "" || got.CoverArtID != "cover-album" || got.DurationMs != 1000 {
		t.Fatalf("album peer = %+v", got)
	}

	// An artist seed scores the artist's playable tracks too.
	tracks, err = local.SimilarLocalTracks(ctx, recommend.TrackSeed{Artist: "Alpha"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) == 0 || tracks[0].Title != "Seed" {
		t.Fatalf("artist seed tracks = %v", titles(tracks))
	}

	// With no library, there is nothing to play.
	none := recommend.NewPlayableSimilarity(st.Q(), func() recommend.PlayableLibrary { return nil })
	if tracks, err := none.SimilarLocalTracks(ctx, recommend.TrackSeed{Artist: "Alpha", Title: "Seed"}, 10); err != nil || len(tracks) != 0 {
		t.Fatalf("no library: %v, %v", titles(tracks), err)
	}
}
