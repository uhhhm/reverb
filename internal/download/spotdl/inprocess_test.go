package spotdl

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

// stubSpotDL stands in for the spotdl package: it logs its arguments, answers
// --version, and otherwise reports a download the way spotDL prints one.
const stubSpotDL = pyruntest.ArgLog + `
if "--version" in sys.argv:
    print("4.4.3")
    sys.exit(0)
print('Downloading "Band - Song": 50%')
print("Downloaded: out.m4a")
`

func inProcess(t *testing.T) (*Adapter, func() [][]string) {
	t.Helper()
	h := pyruntest.Host(t, map[string]string{"spotdl": stubSpotDL})
	logPath := filepath.Join(t.TempDir(), "args")
	h.Env = append(h.Env, "REVERB_TEST_ARGLOG="+logPath)
	a := NewInProcess(h)
	if err := a.Init(map[string]any{"output_dir": t.TempDir(), "binary_path": "/nonexistent/spotdl"}); err != nil {
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

// Every tier keeps the source's own stream on a phone.
func TestInProcessKeepsSourceNativeQuality(t *testing.T) {
	a, calls := inProcess(t)
	if _, err := a.Start(context.Background(), core.DownloadRequest{
		Source: "spotify", ExternalID: "e1", Artist: "Band", Title: "Song", Quality: core.QualityLow,
	}, func(int) {}); err != nil {
		t.Fatal(err)
	}
	got := calls()
	if len(got) == 0 {
		t.Fatal("spotdl never ran")
	}
	args := strings.Join(got[len(got)-1], " ")
	if !strings.Contains(args, "--format m4a --bitrate disable") {
		t.Fatalf("args transcode: %s", args)
	}
}
