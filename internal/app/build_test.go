package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/library/localfiles"
)

func buildForTest(t *testing.T, opts Options) *Runtime {
	t.Helper()
	if opts.Getenv == nil {
		// An empty environment: no adapters configured, so Build wires no library
		// and cannot spawn Navidrome.
		opts.Getenv = func(string) string { return "" }
	}
	if opts.DBPath == "" {
		opts.DBPath = t.TempDir() + "/app.db"
	}
	rt, err := Build(context.Background(), opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(rt.Close)
	return rt
}

// Both entry points build their Deps here, so a dependency wired once is live in
// both binaries. This is the regression guard for the drift that made external
// streaming, live adapter reload and the real resolver silently absent from the
// desktop build: each of these was present in one hand-written root and missing
// from the other, with no compile error.
func TestBuildWiresDepsThatUsedToDriftBetweenRoots(t *testing.T) {
	rt := buildForTest(t, Options{Version: "test"})

	if rt.Deps.ExternalStream == nil {
		t.Error("ExternalStream is nil — playing an un-owned track returns 503")
	}
	if rt.Deps.Reload == nil {
		t.Error("Reload is nil — adapter changes need a restart to take effect")
	}
	if rt.Deps.ConfigDirty == nil {
		t.Error("ConfigDirty is nil — the restart-to-apply banner never shows")
	}
	if rt.Deps.Resolver == nil {
		t.Error("Resolver is nil — canonical ids cannot be addressed")
	}
	if rt.Deps.Auth == nil || rt.Deps.Adapters == nil || rt.Deps.Events == nil {
		t.Error("core deps missing")
	}
	// The desktop root previously built its resolver with a hardcoded nil
	// rematcher, so nothing ever re-matched after a reload.
	if rt.Reloader == nil {
		t.Fatal("Reloader is nil")
	}
	if rt.Reloader.MatcherProvider() == nil {
		t.Error("matcher provider is nil — the resolver can never follow a reload")
	}
}

// Options carry what genuinely differs between the two binaries; the SPA reads
// Desktop to enable desktop-only affordances.
func TestBuildAppliesOptions(t *testing.T) {
	rt := buildForTest(t, Options{Version: "1.2.3", UpdateRepo: "o/r", Dev: true, Desktop: true})

	if rt.Deps.Version != "1.2.3" || rt.Deps.UpdateRepo != "o/r" {
		t.Errorf("version/repo = %q/%q", rt.Deps.Version, rt.Deps.UpdateRepo)
	}
	if !rt.Deps.Dev || !rt.Deps.Desktop {
		t.Errorf("dev/desktop = %v/%v", rt.Deps.Dev, rt.Deps.Desktop)
	}
	if rt.Deps.DataDir == "" {
		t.Error("DataDir empty — playlist cover uploads are disabled")
	}
}

// Build hands back the store it opened so the caller owns the lifecycle, and
// starting the long-running work is a separate call — the desktop smoke test
// builds this same root, and a Navidrome started during Build would fight the
// real app for the fixed 4533 port.
func TestBuildReturnsAnUnstartedRuntime(t *testing.T) {
	rt := buildForTest(t, Options{Version: "test"})
	if rt.Store == nil {
		t.Error("Build must hand back the store it opened")
	}
	if rt.Bundle.Manager != nil && rt.Deps.Downloads == nil {
		t.Error("a built manager must be wired into Deps")
	}
	// StartBackground is what starts things; calling it must be safe on a runtime
	// with no adapters configured.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt.StartBackground(ctx)
}

func TestBuildRequiresGetenv(t *testing.T) {
	if _, err := Build(context.Background(), Options{DBPath: t.TempDir() + "/a.db"}); err == nil {
		t.Fatal("want an error when Getenv is nil")
	}
}

// A phone is a reduced Device (ADR 0003): its library is a folder of files
// rather than a Navidrome, and it carries none of the desktop's executables.
// Everything that replicates is still wired, since that is the point of it.
func TestPhoneProfileBuildsWithoutDesktopServices(t *testing.T) {
	py := &recordingPython{}
	rt := buildForTest(t, Options{Version: "test", Profile: ProfilePhone, Python: py})

	if rt.Bundle.Supervisor != nil {
		t.Error("a phone has no Navidrome to supervise")
	}
	if rt.Bundle.Library == nil || rt.Bundle.Library.Name() != localfiles.Name {
		t.Fatalf("library = %v, want the localfiles adapter", rt.Bundle.Library)
	}
	if got := rt.Deps.MusicDir; got != filepath.Join(rt.Deps.DataDir, "music") {
		t.Errorf("MusicDir = %q, want the data directory's music folder", got)
	}
	// The phone's downloaders are yt-dlp and spotDL run as Python modules,
	// under the names a pasted link or a configured instance asks for.
	names := rt.Deps.Downloader.Names()
	if strings.Join(names, ",") != "spotdl,ytdlp" {
		t.Errorf("phone downloaders = %v, want spotdl and ytdlp", names)
	}
	for _, name := range names {
		p, err := rt.Deps.Downloader.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.TestConnection(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if strings.Join(py.modules, ",") != "spotdl,yt_dlp" {
		t.Errorf("the phone's downloaders ran %v through its Python, want spotdl and yt_dlp", py.modules)
	}
	instances, err := rt.Store.Q().ListAdapterInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 0 {
		t.Errorf("a phone seeded adapter rows: %+v", instances)
	}
	if rt.Deps.ExternalStream == nil {
		t.Error("external streaming is not wired; a phone resolves through its Python")
	}
	if rt.Deps.PortableMigration != nil {
		t.Error("portable-name migration is a desktop action")
	}
	// Managed playlists hang off the download manager, so it is built even
	// with nothing to download with.
	if rt.Bundle.Sync == nil || rt.Bundle.Manager == nil {
		t.Error("the playlist service did not come up")
	}
	if rt.Deps.Pairing == nil || rt.Deps.SyncStore == nil || rt.Deps.SyncEmit == nil {
		t.Error("replication is not wired")
	}
}

// The desktop and server keep what they had: Navidrome, and no folder library.
func TestDesktopProfileIsUnchanged(t *testing.T) {
	rt := buildForTest(t, Options{Version: "test"})
	if rt.Bundle.Supervisor == nil {
		t.Error("the desktop profile lost its Navidrome supervisor")
	}
	for _, n := range rt.Deps.Lib.Names() {
		if n == localfiles.Name {
			t.Error("the folder library is offered on the desktop")
		}
	}
	if names := rt.Deps.Downloader.Names(); strings.Join(names, ",") != "lidarr,spotdl,ytdlp" {
		t.Errorf("desktop downloaders = %v", names)
	}
}

// recordingPython is a Python runner that records which modules were run.
type recordingPython struct{ modules []string }

func (r *recordingPython) RunModule(_ context.Context, module string, _ []string, onLine func(string)) error {
	r.modules = append(r.modules, module)
	onLine("1.0")
	return nil
}
