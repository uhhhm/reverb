package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// requireXDG skips a test whose expectations are the Linux XDG layout.
// os.UserConfigDir is platform-specific, so on macOS the same code correctly
// returns ~/Library/Application Support.
func requireXDG(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skipf("XDG config layout is linux-specific; GOOS=%s", runtime.GOOS)
	}
}

func TestResolveDesktopDB_XDGConfigDir(t *testing.T) {
	requireXDG(t)
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("REVERB_DB", "")
	// Ensure HOME is set so UserConfigDir does not error via fallback.
	// XDG_CONFIG_HOME takes precedence, HOME value irrelevant.
	t.Setenv("HOME", tmp)

	got := ResolveDesktopDB()
	want := filepath.Join(tmp, "reverb", "reverb.db")
	if got != want {
		t.Fatalf("ResolveDesktopDB XDG: got %q want %q", got, want)
	}
}

func TestResolveDesktopDB_XDGHomeFallback(t *testing.T) {
	requireXDG(t)
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("REVERB_DB", "")

	got := ResolveDesktopDB()
	want := filepath.Join(home, ".config", "reverb", "reverb.db")
	if got != want {
		t.Fatalf("ResolveDesktopDB HOME fallback: got %q want %q", got, want)
	}
}

func TestResolveDesktopDB_EnvOverride(t *testing.T) {
	t.Setenv("REVERB_DB", "/custom/override.db")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	got := ResolveDesktopDB()
	if got != "/custom/override.db" {
		t.Fatalf("env override: got %q want %q", got, "/custom/override.db")
	}
}

func TestResolveDesktopDB_FallbackOnError(t *testing.T) {
	t.Setenv("REVERB_DB", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	got := ResolveDesktopDB()
	if got != "./data/reverb.db" {
		t.Fatalf("fallback: got %q want %q", got, "./data/reverb.db")
	}
}

func TestResolveDesktopDataDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)
	t.Setenv("REVERB_DB", "")

	db := ResolveDesktopDB()
	dir := ResolveDesktopDataDir()
	want := filepath.Dir(db)
	if dir != want {
		t.Fatalf("ResolveDesktopDataDir: got %q want %q", dir, want)
	}
	// Also verify it's the reverb data dir. The parent is platform-specific,
	// so only the XDG layout is pinned exactly.
	if runtime.GOOS != "linux" {
		return
	}
	if dir != filepath.Join(tmp, "reverb") {
		t.Fatalf("data dir mismatch: got %q want %q", dir, filepath.Join(tmp, "reverb"))
	}
}

func TestResolveDesktopDataDir_EnvOverride(t *testing.T) {
	t.Setenv("REVERB_DB", "/tmp/foo/bar.db")
	got := ResolveDesktopDataDir()
	want := "/tmp/foo"
	if got != want {
		t.Fatalf("data dir env: got %q want %q", got, want)
	}
}

func TestResolveDesktopDownloadDir_Creation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := ResolveDesktopDownloadDir()
	want := filepath.Join(home, "Music", "Reverb")
	if got != want {
		t.Fatalf("download dir: got %q want %q", got, want)
	}
	if st, err := os.Stat(want); err != nil || !st.IsDir() {
		t.Fatalf("download dir not created: %v stat %v", want, err)
	}
}

func TestResolveDesktopDownloadDir_Fallback(t *testing.T) {
	work := t.TempDir()
	chdir(t, work)
	t.Setenv("HOME", "")

	got := ResolveDesktopDownloadDir()
	if got != "./music" {
		t.Fatalf("fallback download dir: got %q want %q", got, "./music")
	}
	if st, err := os.Stat("./music"); err != nil || !st.IsDir() {
		t.Fatalf("fallback download dir not created: %v", err)
	}
}

func TestMaybeMigrateLegacyDB_CopiesWhenMissing(t *testing.T) {
	work := t.TempDir()
	chdir(t, work)
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVERB_DB", "")

	dest := ResolveDesktopDB()
	if _, err := os.Stat(dest); err == nil {
		t.Fatalf("dest should not exist before migration")
	}

	legacy := filepath.Join(work, "data", "reverb.db")
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	content := []byte("legacy-data-123")
	if err := os.WriteFile(legacy, content, 0644); err != nil {
		t.Fatal(err)
	}

	if err := MaybeMigrateLegacyDB(); err != nil {
		t.Fatalf("MaybeMigrateLegacyDB error: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("dest not created: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("dest content mismatch: got %q want %q", string(got), string(content))
	}
}

func TestMaybeMigrateLegacyDB_NoOverwriteWhenExists(t *testing.T) {
	work := t.TempDir()
	chdir(t, work)
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVERB_DB", "")

	dest := ResolveDesktopDB()
	legacy := filepath.Join(work, "data", "reverb.db")

	// create legacy with content A
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	// create dest with content B
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := MaybeMigrateLegacyDB(); err != nil {
		t.Fatalf("MaybeMigrateLegacyDB error: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing" {
		t.Fatalf("dest was overwritten: got %q want %q", string(got), "existing")
	}
	// legacy should remain untouched
	lgot, _ := os.ReadFile(legacy)
	if string(lgot) != "legacy" {
		t.Fatalf("legacy changed")
	}
}

func TestMaybeMigrateLegacyDB_NoLegacyNoOp(t *testing.T) {
	work := t.TempDir()
	chdir(t, work)
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("REVERB_DB", "")

	dest := ResolveDesktopDB()
	// ensure legacy does not exist
	legacy := filepath.Join(work, "data", "reverb.db")
	_ = os.RemoveAll(legacy)
	_ = os.RemoveAll(dest)

	if err := MaybeMigrateLegacyDB(); err != nil {
		t.Fatalf("MaybeMigrateLegacyDB error: %v", err)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatalf("dest should not be created when legacy missing")
	}
}

func TestMaybeMigrateLegacyDB_SamePathNoCopy(t *testing.T) {
	work := t.TempDir()
	chdir(t, work)
	// Force fallback so dest == legacy == ./data/reverb.db
	t.Setenv("REVERB_DB", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")

	dest := ResolveDesktopDB()
	if dest != "./data/reverb.db" {
		t.Fatalf("expected fallback dest, got %q", dest)
	}
	// create legacy
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}
	// Should not error and not truncate
	if err := MaybeMigrateLegacyDB(); err != nil {
		t.Fatalf("MaybeMigrateLegacyDB error: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "same" {
		t.Fatalf("same path handling corrupted file: got %q", string(got))
	}
}

func TestMaybeMigrateLegacyDB_EnvOverrideDest(t *testing.T) {
	work := t.TempDir()
	chdir(t, work)
	customDest := filepath.Join(t.TempDir(), "custom.db")
	t.Setenv("REVERB_DB", customDest)
	// XDG should be ignored when REVERB_DB set
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	legacy := filepath.Join(work, "data", "reverb.db")
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("via-env"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := MaybeMigrateLegacyDB(); err != nil {
		t.Fatalf("MaybeMigrateLegacyDB error: %v", err)
	}
	got, err := os.ReadFile(customDest)
	if err != nil {
		t.Fatalf("custom dest not created: %v", err)
	}
	if string(got) != "via-env" {
		t.Fatalf("custom dest content mismatch: got %q", string(got))
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir %q: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}
