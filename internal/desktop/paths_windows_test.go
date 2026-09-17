//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// The Windows filesystem contract: the database belongs in the roaming
// application-data directory, not beside the executable, so it survives a
// reinstall and is backed up with the rest of the user's profile.
func TestResolveDesktopDBUsesRoamingAppData(t *testing.T) {
	appData := t.TempDir()
	setConfigHome(t, appData)
	setHome(t, t.TempDir())
	t.Setenv("REVERB_DB", "")

	got := ResolveDesktopDB()
	want := filepath.Join(appData, "reverb", "reverb.db")
	if got != want {
		t.Fatalf("ResolveDesktopDB: got %q want %q", got, want)
	}
}

// Downloads land in the user's Music folder under the app's own name, the same
// relative layout as the other platforms.
func TestResolveDesktopDownloadDirUsesTheUserProfileMusicFolder(t *testing.T) {
	profile := t.TempDir()
	setHome(t, profile)

	got := ResolveDesktopDownloadDir()
	want := filepath.Join(profile, "Music", "Reverb")
	if got != want {
		t.Fatalf("ResolveDesktopDownloadDir: got %q want %q", got, want)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Fatalf("download directory not created: %v", err)
	}
}

// An explicit override still wins over the Windows defaults: the desktop
// contract is flags, then environment, then per-user locations.
func TestWindowsDefaultsYieldToExplicitOverrides(t *testing.T) {
	setConfigHome(t, t.TempDir())
	setHome(t, t.TempDir())

	db := filepath.Join(t.TempDir(), "elsewhere.db")
	t.Setenv("REVERB_DB", db)
	if got := ResolveDesktopDB(); got != db {
		t.Fatalf("REVERB_DB override: got %q want %q", got, db)
	}
	if got := ResolveDesktopDataDir(); got != filepath.Dir(db) {
		t.Fatalf("data dir: got %q want %q", got, filepath.Dir(db))
	}
}
