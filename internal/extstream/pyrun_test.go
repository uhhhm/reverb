package extstream

import (
	"context"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/pyrun"
	"github.com/uhhhm/reverb/internal/pyrun/pyruntest"
)

// stubYtDlp answers a resolve the way yt-dlp does: a search prints the video
// id, a format extraction prints the media URL.
const stubYtDlp = `import sys
if "--flat-playlist" in sys.argv:
    print("vid123")
else:
    print("https://media.example/vid123.webm?expire=4102444800")
`

// External playback on a phone resolves through the same Python runner the
// downloaders use, with no yt-dlp executable anywhere.
func TestResolveThroughThePythonRunner(t *testing.T) {
	h := pyruntest.Host(t, map[string]string{"yt_dlp": stubYtDlp})
	lookup := &fakeLookup{track: core.ExternalResult{Artist: "Band", Title: "Song"}}
	s := New(lookup, WithRunner(pyrun.Module(h, pyrun.YtDlp)), WithBinary("/nonexistent/yt-dlp"))
	got, err := s.Resolve(context.Background(), "deezer", "42")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://media.example/vid123.webm?expire=4102444800" {
		t.Fatalf("resolved %q", got)
	}
}
