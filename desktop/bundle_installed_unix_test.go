//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstalledBundle checks a real, extracted install bundle rather than a
// fake layout: every tool the app needs resolves from inside the bundle through
// the app's own lookup, and runs, with none of them on PATH. package-linux.sh
// and package-mac.sh run it, through verify-bundle.sh, against what they are
// about to ship:
//
//	desktop/tools/verify-bundle.sh /tmp/x/Reverb/reverb-desktop
func TestInstalledBundle(t *testing.T) {
	exe := os.Getenv("REVERB_BUNDLE_EXE")
	if exe == "" {
		t.Skip("REVERB_BUNDLE_EXE is not set; the packaging scripts set it")
	}
	exe, err := filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !isExecutable(exe) {
		t.Fatalf("REVERB_BUNDLE_EXE=%q is not an executable", exe)
	}
	root := bundleRoot(exe)

	orig := executablePath
	executablePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executablePath = orig })
	// Resolution must not depend on the working tree the test runs from.
	t.Chdir(t.TempDir())
	t.Setenv("PATH", shellOnlyPath(t))
	for _, key := range []string{"REVERB_NAVIDROME_BIN", "REVERB_SPOTDL_PATH", "REVERB_YTDLP_PATH", "REVERB_DENO_PATH", "REVERB_YTDLP_PYTHON"} {
		t.Setenv(key, "")
	}

	ffmpeg, navidrome, spotdl, deno, ytdlp := ResolveBundledTools()
	for _, tool := range []struct {
		name, path string
		version    []string
	}{
		{"ffmpeg", ffmpeg, []string{"-version"}},
		{"navidrome", navidrome, []string{"--version"}},
		{"deno", deno, []string{"--version"}},
		{"spotdl", spotdl, []string{"--version"}},
		{"yt-dlp", ytdlp, []string{"--version"}},
		{"python", ResolveBundledPython(), []string{"--version"}},
	} {
		if !strings.HasPrefix(tool.path, root+string(filepath.Separator)) {
			t.Errorf("%s resolved to %q, outside the bundle %s", tool.name, tool.path, root)
			continue
		}
		out, err := exec.Command(tool.path, tool.version...).CombinedOutput()
		if err != nil {
			t.Errorf("%s %s: %v\n%s", tool.path, strings.Join(tool.version, " "), err, out)
			continue
		}
		t.Logf("%s: %s", tool.name, firstLine(out))
	}
}

// bundleRoot is the directory an install bundle occupies: the .app for a
// binary in Contents/MacOS, otherwise the binary's own directory.
func bundleRoot(exe string) string {
	dir := filepath.Dir(exe)
	if filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
		return filepath.Dir(filepath.Dir(dir))
	}
	return dir
}

// shellOnlyPath returns a PATH holding just what the tools' shell wrappers
// call, so a system copy of any tool cannot stand in for the bundled one.
func shellOnlyPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, cmd := range []string{"sh", "dirname", "readlink"} {
		target, err := exec.LookPath(cmd)
		if err != nil {
			t.Fatalf("the tool wrappers need %s: %v", cmd, err)
		}
		if err := os.Symlink(target, filepath.Join(dir, cmd)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return line
}
