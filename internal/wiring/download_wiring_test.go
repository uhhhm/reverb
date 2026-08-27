package wiring

import (
	"testing"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/download/spotdl"
	"github.com/uhhhm/reverb/internal/download/ytdlp"
	"github.com/uhhhm/reverb/internal/registry"
	"github.com/uhhhm/reverb/internal/store/db"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestBuildDownloadersEnabledOnly(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	instances := []db.AdapterInstance{
		{Type: "downloader", Name: "spotdl", Enabled: 1, ConfigJson: `{"output_dir":"/music"}`},
		{Type: "downloader", Name: "spotdl", Enabled: 0, ConfigJson: `{"output_dir":"/music2"}`},
		{Type: "library", Name: "subsonic", Enabled: 1, ConfigJson: `{}`},
	}
	out := BuildDownloaders(reg, instances, env(nil))
	if len(out) != 1 {
		t.Fatalf("want 1 enabled downloader, got %d", len(out))
	}
	if out[0].Downloader.Name() != "spotdl" {
		t.Fatalf("name = %q", out[0].Downloader.Name())
	}
}

func TestBuildDownloadersEnvOverrideAndSkipOnBadConfig(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	instances := []db.AdapterInstance{
		// Missing output_dir in config; env supplies it → must succeed.
		{Type: "downloader", Name: "spotdl", Enabled: 1, ConfigJson: `{}`},
		// Unknown adapter → warn-and-skip, not a panic.
		{Type: "downloader", Name: "ghost", Enabled: 1, ConfigJson: `{}`},
	}
	out := BuildDownloaders(reg, instances, env(map[string]string{"REVERB_DOWNLOAD_DIR": "/from/env"}))
	if len(out) != 1 {
		t.Fatalf("want 1 downloader (env-supplied dir), got %d", len(out))
	}
}

func TestBuildDownloadersBundledSpotdlDefault(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	// No downloader instance configured + REVERB_DOWNLOAD_DIR set (as the image
	// sets it) → the bundled spotDL default is injected.
	instances := []db.AdapterInstance{{Type: "library", Name: "subsonic", Enabled: 1, ConfigJson: `{}`}}
	out := BuildDownloaders(reg, instances, env(map[string]string{"REVERB_DOWNLOAD_DIR": "/music"}))
	if len(out) != 1 || out[0].Downloader.Name() != "spotdl" {
		t.Fatalf("want 1 bundled spotdl default, got %d", len(out))
	}
}

func TestBuildDownloadersNoDefaultWhenInstancePresent(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	// A DISABLED downloader instance means the user manages it → do NOT inject the
	// bundled default even though no downloader ends up enabled.
	instances := []db.AdapterInstance{
		{Type: "downloader", Name: "spotdl", Enabled: 0, ConfigJson: `{"output_dir":"/music"}`},
	}
	out := BuildDownloaders(reg, instances, env(map[string]string{"REVERB_DOWNLOAD_DIR": "/music"}))
	if len(out) != 0 {
		t.Fatalf("want 0 (respect user's disabled instance), got %d", len(out))
	}
}

func TestBuildDownloadersNoDefaultWithoutDir(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	// No env (e.g. local dev) → no bundled default, unchanged behavior.
	out := BuildDownloaders(reg, nil, env(nil))
	if len(out) != 0 {
		t.Fatalf("want 0 without REVERB_DOWNLOAD_DIR, got %d", len(out))
	}
}

func TestBuildDownloadersBundledYtdlpDefault(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	reg.Register("ytdlp", func() registry.Plugin { return ytdlp.New() })
	// Nothing configured: both bundled downloaders are injected, spotDL first and
	// ytdlp behind it, so pasted links can prefer ytdlp by name.
	out := BuildDownloaders(reg, nil, env(map[string]string{"REVERB_DOWNLOAD_DIR": "/music"}))
	if len(out) != 2 {
		t.Fatalf("want 2 bundled defaults, got %d", len(out))
	}
	if out[0].Downloader.Name() != "spotdl" || out[1].Downloader.Name() != "ytdlp" {
		t.Fatalf("order = %q, %q", out[0].Downloader.Name(), out[1].Downloader.Name())
	}
	if out[1].Order[core.GranularityTrack] != ytdlpDefaultOrder {
		t.Fatalf("ytdlp track order = %d, want %d", out[1].Order[core.GranularityTrack], ytdlpDefaultOrder)
	}
}

// The bundled yt-dlp fallback is injected even when the user HAS a configured
// downloader, because spotDL cannot service a pasted YouTube URL.
func TestBuildDownloadersYtdlpInjectedAlongsideConfiguredSpotdl(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("spotdl", func() registry.Plugin { return spotdl.New() })
	reg.Register("ytdlp", func() registry.Plugin { return ytdlp.New() })
	instances := []db.AdapterInstance{
		{Type: "downloader", Name: "spotdl", Enabled: 1, ConfigJson: `{"output_dir":"/music"}`},
	}
	out := BuildDownloaders(reg, instances, env(map[string]string{"REVERB_DOWNLOAD_DIR": "/music"}))
	if len(out) != 2 {
		t.Fatalf("want configured spotdl + injected ytdlp, got %d", len(out))
	}
	if out[1].Downloader.Name() != "ytdlp" {
		t.Fatalf("want ytdlp injected, got %q", out[1].Downloader.Name())
	}
}

// A user who configured ytdlp themselves (even disabled) owns that choice — no
// injection on top of it.
func TestBuildDownloadersNoYtdlpInjectionWhenUserConfiguredIt(t *testing.T) {
	reg := registry.NewRegistry("downloader")
	reg.Register("ytdlp", func() registry.Plugin { return ytdlp.New() })
	instances := []db.AdapterInstance{
		{Type: "downloader", Name: "ytdlp", Enabled: 0, ConfigJson: `{"output_dir":"/music"}`},
	}
	out := BuildDownloaders(reg, instances, env(map[string]string{"REVERB_DOWNLOAD_DIR": "/music"}))
	if len(out) != 0 {
		t.Fatalf("want 0 (respect disabled ytdlp instance), got %d", len(out))
	}
}
