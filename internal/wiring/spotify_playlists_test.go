package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download/spotdl"
	"github.com/uhhhm/reverb/internal/events"
	"github.com/uhhhm/reverb/internal/playlistsync"
	"github.com/uhhhm/reverb/internal/registry"
)

// playlistSource is a Spotify search provider that can read playlists.
type playlistSource struct{ stubSource }

func (playlistSource) GetPlaylist(_ context.Context, id string) (core.ExternalPlaylist, error) {
	return core.ExternalPlaylist{Source: "spotify", ExternalID: id, Name: "from provider"}, nil
}

// fakeReader stands in for the spotDL playlist reader.
type fakeReader struct {
	calls int
	err   error
}

func (f *fakeReader) GetPlaylist(_ context.Context, id string) (core.ExternalPlaylist, error) {
	f.calls++
	if f.err != nil {
		return core.ExternalPlaylist{}, f.err
	}
	return core.ExternalPlaylist{Source: "spotify", ExternalID: id, Name: "from spotDL"}, nil
}

func playlistBuilder(t *testing.T, withProvider bool, reader *fakeReader) *playlistsync.Service {
	t.Helper()
	st := newTestStore(t)
	addInstance(t, st, "library", "subsonic", `{"url":"http://x"}`)
	addInstance(t, st, "downloader", "spotdl", `{"output_dir":"/music"}`)
	if withProvider {
		addInstance(t, st, "search", "spotify", `{"client_id":"c"}`)
	}
	libReg := registry.NewRegistry("library")
	libReg.Register("subsonic", func() registry.Plugin { return &stubLib{} })
	searchReg := registry.NewRegistry("search")
	searchReg.Register("spotify", func() registry.Plugin { return &playlistSource{} })
	dlReg := registry.NewRegistry("downloader")
	dlReg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	b := NewBuilder(libReg, searchReg, dlReg, st.Q(), st, events.New(), nil, func(string) string { return "" }, t.TempDir())
	b.SetSpotifyPlaylistFallback(reader)
	bundle, err := b.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bundle.Manager.Stop)
	if bundle.Sync == nil {
		t.Fatal("no playlist service")
	}
	return bundle.Sync
}

func TestSpotifyPlaylistsReadThroughSpotDLWithoutProvider(t *testing.T) {
	reader := &fakeReader{}
	pl, err := playlistBuilder(t, false, reader).Preview(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if pl.Name != "from spotDL" {
		t.Fatalf("read %q, want the spotDL reader's playlist", pl.Name)
	}
}

func TestSpotifyProviderIsPreferredOverSpotDL(t *testing.T) {
	reader := &fakeReader{}
	pl, err := playlistBuilder(t, true, reader).Preview(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if pl.Name != "from provider" || reader.calls != 0 {
		t.Fatalf("read %q with %d spotDL call(s), want the provider's and none", pl.Name, reader.calls)
	}
}

// Without spotDL there is no way to read Spotify, which is what the API
// reports as Spotify not being configured.
func TestMissingSpotDLIsSpotifyNotConfigured(t *testing.T) {
	reader := &fakeReader{err: spotdl.ErrNotInstalled}
	_, err := playlistBuilder(t, false, reader).Preview(context.Background(), "abc")
	if !errors.Is(err, playlistsync.ErrSpotifyNotConfigured) {
		t.Fatalf("err = %v, want ErrSpotifyNotConfigured", err)
	}
}
