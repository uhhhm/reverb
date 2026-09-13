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
