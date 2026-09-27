package extstream

import (
	"context"
	"os"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download/ytdlp"
)

// isolateConfigDir points os.UserConfigDir at a temp dir on every platform, so
// the test reads and writes only its own cookies file.
func isolateConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir)
}

func cookiesArg(args []string) (string, bool) {
	for i, a := range args {
		if a == "--cookies" && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// The owner pastes cookies into the yt-dlp adapter settings once. A stream must
// use that same file, including when the cookies are saved after the stream
// service was built.
func TestStreamUsesTheCookiesTheYtdlpAdapterSaved(t *testing.T) {
	isolateConfigDir(t)
	const pasted = "# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tabc\n"

	r := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/a"}}
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	svc := NewFromEnv(l, func(string) string { return "" }, WithRunner(r))

	adapter := ytdlp.New()
	if err := adapter.Init(map[string]any{"output_dir": t.TempDir(), "youtube_cookies": pasted}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, err := svc.Resolve(context.Background(), "deezer", "1"); err != nil {
		t.Fatal(err)
	}
	for stage, args := range map[string][]string{"search": r.searchArgs, "media": r.gotArgs} {
		path, ok := cookiesArg(args)
		if !ok {
			t.Fatalf("%s stage ran without --cookies: %q", stage, args)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s stage --cookies %q: %v", stage, path, err)
		}
		if string(got) != pasted {
			t.Fatalf("%s stage --cookies %q holds %q, not the pasted cookies", stage, path, got)
		}
	}

}

// A YouTube result already names its video. Searching by artist and title
// could land on a different upload.
func TestYouTubeTrackResolvesByItsVideoID(t *testing.T) {
	l := &fakeLookup{track: core.ExternalResult{Artist: "Air", Title: "Alone in Kyoto"}}
	r := &fakeRunner{lines: []string{"https://rr1.googlevideo.com/a"}}
	if _, err := newService(t, l, r).ResolveHinted(context.Background(), "youtube", "dQw4w9WgXcQ", "Air", "Alone in Kyoto"); err != nil {
		t.Fatal(err)
	}
	if n := r.searchCount(); n != 0 {
		t.Fatalf("searched %d times for a track with a known video id", n)
	}
	if l.calls != 0 {
		t.Fatalf("looked the track up %d times", l.calls)
	}
	want := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	if r.gotArgs[len(r.gotArgs)-1] != want {
		t.Fatalf("media stage target = %q, want %q", r.gotArgs[len(r.gotArgs)-1], want)
	}
}
