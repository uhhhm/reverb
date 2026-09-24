package ytdlp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download"
	"github.com/uhhhm/reverb/internal/pyrun/pyruntest"
)

// stubYtDlp stands in for the yt_dlp package: it logs its arguments, answers
// --version, and otherwise reports a download the way yt-dlp prints one.
const stubYtDlp = pyruntest.ArgLog + `
if "--version" in sys.argv:
    print("2026.09.01")
    sys.exit(0)
if "--skip-download" in sys.argv:
    print("129.5")
    sys.exit(0)
print("[youtube] Extracting URL")
print("[download]  12.0% of ~4.00MiB")
print("[download] 100% of 4.00MiB")
print("[ExtractAudio] Destination: out.opus")
`

// inProcess builds the in-process adapter over host Python running the stub,
// and returns it with a function reading back every invocation's arguments.
func inProcess(t *testing.T) (*Adapter, func() [][]string) {
	t.Helper()
	h := pyruntest.Host(t, map[string]string{"yt_dlp": stubYtDlp})
	logPath := filepath.Join(t.TempDir(), "args")
	h.Env = append(h.Env, "REVERB_TEST_ARGLOG="+logPath)
	a := NewInProcess(h)
	if err := a.Init(map[string]any{"output_dir": t.TempDir(), "binary_path": "/nonexistent/yt-dlp"}); err != nil {
		t.Fatal(err)
	}
	return a, func() [][]string {
		raw, _ := os.ReadFile(logPath)
		var calls [][]string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line != "" {
				calls = append(calls, strings.Split(line, "\x00"))
			}
		}
		return calls
	}
}

func TestInProcessConformance(t *testing.T) {
	a, _ := inProcess(t)
	download.RunConformance(t, a)
}

// The module runs whatever binary path is configured: there is no executable.
func TestInProcessRunsTheModule(t *testing.T) {
	a, calls := inProcess(t)
	if err := a.TestConnection(context.Background()); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if got := calls(); len(got) != 1 || got[0][0] != "--version" {
		t.Fatalf("calls = %q", got)
	}
	for _, f := range a.ConfigSchema().Fields {
		if f.Key == "binary_path" {
			t.Error("an in-process downloader offers a binary path")
		}
	}
}

// A phone keeps the stream the source served: no tier transcodes it, and so
// no bitrate probe is spent deciding whether to.
func TestInProcessKeepsSourceNativeQuality(t *testing.T) {
	a, calls := inProcess(t)
	var progress []int
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		Artist: "Band", Title: "Song", Quality: core.QualityLow,
	}, func(p int) { progress = append(progress, p) }); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) != 1 {
		t.Fatalf("want one invocation (no probe), got %q", got)
	}
	args := strings.Join(got[0], " ")
	if !strings.Contains(args, "--audio-format best") || strings.Contains(args, "--audio-quality") {
		t.Fatalf("args transcode: %s", args)
	}
	if len(progress) == 0 || progress[len(progress)-1] != 90 {
		t.Fatalf("progress = %v", progress)
	}
}

// A phone plays what it downloads with AVPlayer, which cannot open WebM or
// Ogg, so it asks for the source's M4A when there is one. The desktop keeps
// taking the best audio, whatever its container.
func TestInProcessPrefersAudioThePhoneCanPlay(t *testing.T) {
	a, calls := inProcess(t)
	if _, err := a.Start(context.Background(), core.DownloadRequest{Artist: "Band", Title: "Song"}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(calls()[0], " "); !strings.Contains(args, "-f bestaudio[ext=m4a]/bestaudio") {
		t.Fatalf("args %s do not prefer M4A", args)
	}

	r := &fakeRunner{lines: []string{"[download] 100.0% of 4.00MiB"}}
	if _, err := newAdapter(t, r, nil).Start(context.Background(), core.DownloadRequest{Artist: "A", Title: "T"}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.argString(), "bestaudio[ext=m4a]") {
		t.Fatalf("the desktop narrowed its format: %s", r.argString())
	}
}
