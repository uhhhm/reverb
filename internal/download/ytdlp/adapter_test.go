package ytdlp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download"
)

// fakeRunner replays canned lines and returns a canned error, recording the args.
type fakeRunner struct {
	lines []string
	err   error

	gotName string
	gotArgs []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string, onLine func(string)) error {
	f.gotName, f.gotArgs = name, args
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, l := range f.lines {
		onLine(l)
	}
	return f.err
}

func (f *fakeRunner) argString() string { return strings.Join(f.gotArgs, " ") }

// newAdapter builds an initialized adapter backed by r.
func newAdapter(t *testing.T, r Runner, cfg map[string]any) *Adapter {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{"output_dir": "/music"}
	}
	a := New().WithRunner(r)
	if err := a.Init(cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return a
}

func TestConformance(t *testing.T) {
	r := &fakeRunner{lines: []string{"[download]  50.0% of 4.00MiB", "[ExtractAudio] Destination: x.mp3"}}
	download.RunConformance(t, newAdapter(t, r, nil))
}

func TestIdentityAndSchema(t *testing.T) {
	a := New()
	if a.Type() != "downloader" || a.Name() != "ytdlp" {
		t.Fatalf("identity = %q/%q", a.Type(), a.Name())
	}
	gs := a.SupportedGranularities()
	if len(gs) != 1 || gs[0] != core.GranularityTrack {
		t.Fatalf("SupportedGranularities() = %v, want [track]", gs)
	}
	var keys []string
	for _, f := range a.ConfigSchema().Fields {
		keys = append(keys, f.Key)
		if f.Key == "youtube_cookies" && !f.Secret {
			t.Error("youtube_cookies must be marked Secret")
		}
	}
	for _, want := range []string{"output_dir", "binary_path", "audio_format", "audio_quality", "youtube_cookies"} {
		if !strings.Contains(strings.Join(keys, ","), want) {
			t.Errorf("ConfigSchema missing %q (got %v)", want, keys)
		}
	}
}

func TestInitRequiresOutputDir(t *testing.T) {
	if err := New().Init(map[string]any{}); err == nil {
		t.Fatal("Init without output_dir must fail")
	}
}

func TestInitDefaultsAndOverrides(t *testing.T) {
	a := newAdapter(t, &fakeRunner{}, map[string]any{"output_dir": "/music"})
	if a.binary != "yt-dlp" || a.audioFormat != "mp3" || a.audioQuality != "0" {
		t.Fatalf("defaults = %q/%q/%q", a.binary, a.audioFormat, a.audioQuality)
	}
	a = newAdapter(t, &fakeRunner{}, map[string]any{
		"output_dir": "/music", "binary_path": "/opt/yt-dlp",
		"audio_format": "opus", "audio_quality": "192K",
	})
	if a.binary != "/opt/yt-dlp" || a.audioFormat != "opus" || a.audioQuality != "192K" {
		t.Fatalf("overrides = %q/%q/%q", a.binary, a.audioFormat, a.audioQuality)
	}
}

func TestCanDownloadHeuristic(t *testing.T) {
	a := newAdapter(t, &fakeRunner{}, nil)
	cases := []struct {
		name string
		req  core.DownloadRequest
		want bool
	}{
		{"artist+title", core.DownloadRequest{Artist: "A", Title: "T"}, true},
		{"manual url only", core.DownloadRequest{ManualURL: "https://youtu.be/abc"}, true},
		{"youtube id only", core.DownloadRequest{Source: "youtube", ExternalID: "abc"}, true},
		{"title only", core.DownloadRequest{Title: "T"}, true},
		{"nothing", core.DownloadRequest{Source: "spotify", ExternalID: "x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.CanDownload(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("CanDownload: %v", err)
			}
			if got != tc.want {
				t.Errorf("CanDownload = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildQueryPriority(t *testing.T) {
	cases := []struct {
		name string
		req  core.DownloadRequest
		want string
	}{
		{"manual url wins", core.DownloadRequest{
			ManualURL: "https://youtu.be/abc", Source: "youtube", ExternalID: "zzz", Artist: "A", Title: "T",
		}, "https://www.youtube.com/watch?v=abc"},
		{"playlist params stripped", core.DownloadRequest{
			ManualURL: "https://www.youtube.com/watch?v=abc&list=RDabc&start_radio=1",
		}, "https://www.youtube.com/watch?v=abc"},
		{"music.youtube normalized", core.DownloadRequest{
			ManualURL: "https://music.youtube.com/watch?v=abc&si=x",
		}, "https://www.youtube.com/watch?v=abc"},
		{"non-youtube url untouched", core.DownloadRequest{
			ManualURL: "https://soundcloud.com/a/b",
		}, "https://soundcloud.com/a/b"},
		{"youtube source id", core.DownloadRequest{
			Source: "youtube", ExternalID: "abc", Artist: "A", Title: "T",
		}, "https://www.youtube.com/watch?v=abc"},
		{"text search fallback", core.DownloadRequest{
			Source: "spotify", ExternalID: "s1", Artist: "A", Title: "T",
		}, "ytsearch1:A T"},
		{"nothing to go on", core.DownloadRequest{Source: "spotify", ExternalID: "s1"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildQuery(tc.req); got != tc.want {
				t.Errorf("buildQuery = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStartWithNothingToSearchFailsAsNoMatch(t *testing.T) {
	a := newAdapter(t, &fakeRunner{}, nil)
	_, err := a.Start(context.Background(), core.DownloadRequest{Source: "spotify", ExternalID: "s1"}, func(int) {})
	var ce download.ClassifiedError
	if !errors.As(err, &ce) || ce.Class != download.ClassNoMatch {
		t.Fatalf("err = %v, want ClassNoMatch ClassifiedError", err)
	}
}

func TestStartArgStructure(t *testing.T) {
	r := &fakeRunner{lines: []string{"[download] 100.0% of 4.00MiB"}}
	a := newAdapter(t, r, map[string]any{
		"output_dir": "/music/", "audio_format": "opus", "audio_quality": "3",
	})
	out, err := a.Start(context.Background(), core.DownloadRequest{
		Source: "youtube", ExternalID: "abc", Artist: "A", Title: "T", Album: "Al",
	}, func(int) {})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if out != "/music/" {
		t.Errorf("output path = %q, want the configured output_dir", out)
	}
	if r.gotName != "yt-dlp" {
		t.Errorf("binary = %q", r.gotName)
	}
	args := r.argString()
	for _, want := range []string{
		"--no-playlist", "--extract-audio", "--audio-format opus", "--audio-quality 3",
		"--embed-metadata", "--output /music/A - T.%(ext)s",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q\ngot: %s", want, args)
		}
	}
	// The query must be the final arg, after a "--" end-of-options separator, so a
	// title starting with "-" can never be read as a flag.
	if n := len(r.gotArgs); n < 2 || r.gotArgs[n-2] != "--" || r.gotArgs[n-1] != "https://www.youtube.com/watch?v=abc" {
		t.Errorf("query must be last, preceded by --; got tail %v", r.gotArgs[max(0, len(r.gotArgs)-2):])
	}
}

func TestStartForcesKnownMetadataTags(t *testing.T) {
	r := &fakeRunner{lines: []string{"[download] 10.0% of 1MiB"}}
	a := newAdapter(t, r, nil)
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		Artist: "A", Title: "T", Album: "Al",
	}, func(int) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	args := r.argString()
	for _, want := range []string{"A:%(meta_artist)s", "T:%(meta_title)s", "Al:%(meta_album)s"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing --parse-metadata %q\ngot: %s", want, args)
		}
	}
}

func TestStartOmitsMetadataTagsWhenUnknown(t *testing.T) {
	r := &fakeRunner{lines: []string{"[download] 10.0% of 1MiB"}}
	a := newAdapter(t, r, nil)
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		ManualURL: "https://youtu.be/abc",
	}, func(int) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if strings.Contains(r.argString(), "--parse-metadata") {
		t.Errorf("must not pass --parse-metadata with no known tags\ngot: %s", r.argString())
	}
	if !strings.Contains(r.argString(), "--output /music/%(title)s.%(ext)s") {
		t.Errorf("expected the %%(title)s output template\ngot: %s", r.argString())
	}
}

func TestOutputTemplateSanitizesPlaceholders(t *testing.T) {
	a := newAdapter(t, &fakeRunner{}, nil)
	got := a.outputTemplate(core.DownloadRequest{Artist: "AC/DC", Title: "100% Free"})
	if got != "/music/AC-DC - 100 Free.%(ext)s" {
		t.Errorf("outputTemplate = %q", got)
	}
}

func TestStartCookiesArgOnlyWhenConfigured(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	r := &fakeRunner{lines: []string{"[download] 10.0% of 1MiB"}}
	a := newAdapter(t, r, nil)
	if _, err := a.Start(context.Background(), core.DownloadRequest{Artist: "A", Title: "T"}, func(int) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if strings.Contains(r.argString(), "--cookies") {
		t.Error("--cookies must be absent when no cookies are configured")
	}

	a = newAdapter(t, r, map[string]any{"output_dir": "/music", "youtube_cookies": "# Netscape HTTP Cookie File\n"})
	if _, err := a.Start(context.Background(), core.DownloadRequest{Artist: "A", Title: "T"}, func(int) {}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !strings.Contains(r.argString(), "--cookies ") {
		t.Errorf("--cookies must be passed when configured\ngot: %s", r.argString())
	}
}

func TestStartProgressScalingAndStages(t *testing.T) {
	r := &fakeRunner{lines: []string{
		"[youtube] abc: Downloading webpage",
		"[download]   0.0% of ~4.00MiB at Unknown B/s",
		"[download]  50.4% of ~4.00MiB at 1.00MiB/s",
		"[download] 100.0% of 4.00MiB in 00:03",
		"[ExtractAudio] Destination: /music/A - T.mp3",
		"[Metadata] Adding metadata to \"/music/A - T.mp3\"",
	}}
	a := newAdapter(t, r, nil)
	var got []int
	if _, err := a.Start(context.Background(), core.DownloadRequest{Artist: "A", Title: "T"}, func(p int) {
		got = append(got, p)
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Byte progress is scaled into 0-85 so post-processing has room above it.
	want := []int{0, 42, 85, 90, 95}
	if len(got) != len(want) {
		t.Fatalf("progress = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("progress = %v, want %v", got, want)
		}
	}
}

func TestStartUnparseableOutputIsNotAnError(t *testing.T) {
	r := &fakeRunner{lines: []string{"total gibberish", "no percentages here"}}
	a := newAdapter(t, r, nil)
	var got []int
	out, err := a.Start(context.Background(), core.DownloadRequest{Artist: "A", Title: "T"}, func(p int) {
		got = append(got, p)
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if out == "" {
		t.Error("expected an output path")
	}
	if len(got) != 1 || got[0] != -1 {
		t.Errorf("progress = %v, want [-1] (indeterminate)", got)
	}
}

func TestStartClassifiesFailures(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		want  download.FailureClass
		inMsg string
	}{
		{"rate limited", "ERROR: unable to download: HTTP Error 429: Too Many Requests",
			download.ClassRateLimited, "rate-limiting"},
		{"bot challenge", "ERROR: Sign in to confirm you're not a bot",
			download.ClassBotChallenge, "sign-in"},
		{"no match", "ERROR: [youtube:search] No video results",
			download.ClassNoMatch, "no result"},
		{"unavailable", "ERROR: [youtube] abc: Video unavailable",
			download.ClassUnavailable, "unavailable"},
		{"ffmpeg missing", "ERROR: ffprobe/avprobe and ffmpeg/avconv not found",
			download.ClassUnknown, "ffmpeg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{lines: []string{tc.line}, err: errors.New("exit status 1")}
			a := newAdapter(t, r, nil)
			_, err := a.Start(context.Background(), core.DownloadRequest{Artist: "A", Title: "T"}, func(int) {})
			var ce download.ClassifiedError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v, want ClassifiedError", err)
			}
			if ce.Class != tc.want {
				t.Errorf("class = %q, want %q", ce.Class, tc.want)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.inMsg) {
				t.Errorf("message %q missing %q", err.Error(), tc.inMsg)
			}
		})
	}
}

func TestStartRespectsCanceledContext(t *testing.T) {
	r := &fakeRunner{lines: []string{"[download] 10.0% of 1MiB"}}
	a := newAdapter(t, r, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Start(ctx, core.DownloadRequest{Artist: "A", Title: "T"}, func(int) {}); err == nil {
		t.Fatal("canceled ctx must surface an error")
	}
}

func TestTestConnectionRunsVersion(t *testing.T) {
	r := &fakeRunner{}
	a := newAdapter(t, r, nil)
	if err := a.TestConnection(context.Background()); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if r.gotName != "yt-dlp" || r.argString() != "--version" {
		t.Errorf("ran %q %v", r.gotName, r.gotArgs)
	}

	r = &fakeRunner{err: errors.New("not found")}
	a = newAdapter(t, r, nil)
	if err := a.TestConnection(context.Background()); err == nil {
		t.Fatal("TestConnection must surface a runner error")
	}
}

// Every flag the adapter passes must actually exist in yt-dlp: a typo'd flag makes
// yt-dlp exit on usage before downloading anything ("no such option").
func TestStartUsesOnlyRealProgressFlags(t *testing.T) {
	r := &fakeRunner{}
	a := newAdapter(t, r, nil)
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		Artist: "A", Title: "T", ManualURL: "https://www.youtube.com/watch?v=abc",
	}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.argString(), "--no-progress-bar") {
		t.Errorf("--no-progress-bar is not a yt-dlp option: %s", r.argString())
	}
	if !strings.Contains(r.argString(), "--newline") {
		t.Errorf("want --newline for line-wise progress: %s", r.argString())
	}
}

// yt-dlp splits --parse-metadata at the first unescaped colon, so a colon in the
// title must be escaped or the tag is silently truncated.
func TestStartEscapesColonsInParseMetadata(t *testing.T) {
	r := &fakeRunner{}
	a := newAdapter(t, r, nil)
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		Artist: "HOYO-MiX", Title: "Lullaby of the New Moon (I) : Somnias a Luna",
		ManualURL: "https://www.youtube.com/watch?v=abc",
	}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	want := `Lullaby of the New Moon (I) \: Somnias a Luna:%(meta_title)s`
	found := false
	for _, arg := range r.gotArgs {
		if arg == want {
			found = true
		}
	}
	if !found {
		t.Errorf("want escaped title arg %q, got args: %v", want, r.gotArgs)
	}
}
