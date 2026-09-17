package updater

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every host's payload shape is checked from whichever host runs the tests, so
// a Windows regression surfaces without a Windows runner.
func TestVerifyExecutablePerPlatform(t *testing.T) {
	big := func(magic string) []byte {
		return append([]byte(magic), bytes.Repeat([]byte{0x41}, 2<<20)...)
	}
	for _, tc := range []struct {
		name, goos string
		body       []byte
		wantErr    bool
	}{
		{"windows PE", "windows", big("MZ\x90\x00"), false},
		{"windows HTML error page", "windows", big("<htm"), true},
		{"linux ELF", "linux", big("\x7fELF"), false},
		{"darwin Mach-O", "darwin", big("\xcf\xfa\xed\xfe"), false},
		{"truncated", "windows", []byte("MZ"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(path, tc.body, 0o755); err != nil {
				t.Fatal(err)
			}
			err := verifyExecutableFor(tc.goos, path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("verifyExecutableFor(%q) = %v, wantErr=%v", tc.goos, err, tc.wantErr)
			}
		})
	}
}

// The Windows release zip carries reverb-desktop.exe; asking for the bare name
// there would leave the updater unable to unpack its own artifact.
func TestPayloadNameCarriesTheWindowsExtension(t *testing.T) {
	if got := payloadName("windows"); got != "reverb-desktop.exe" {
		t.Fatalf("payloadName(windows) = %q", got)
	}
	if got := payloadName("linux"); got != "reverb-desktop" {
		t.Fatalf("payloadName(linux) = %q", got)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "asset.zip")
	if err := os.WriteFile(path, zipOf(t, "reverb-desktop.exe", []byte("payload")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := unzipNamed(path, dir, payloadName("windows"))
	if err != nil {
		t.Fatalf("unzipNamed: %v", err)
	}
	if got, _ := os.ReadFile(out); string(got) != "payload" {
		t.Fatalf("extracted %q", got)
	}
	if _, err := unzipNamed(path, dir, payloadName("linux")); err == nil {
		t.Fatal("a zip holding only the .exe satisfied a request for the bare name")
	}
}

// Windows refuses to delete an image a process still has mapped. The swap must
// still go ahead, against a name that is free.
func TestBackupPathFallsBackWhenTheNameCannotBeFreed(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "reverb-desktop")
	preferred := backupPath(exe)

	if got := freeBackupPath(exe); got != preferred {
		t.Fatalf("with nothing in the way: %q want %q", got, preferred)
	}

	// A non-empty directory stands in for a file the OS will not unlink.
	if err := os.MkdirAll(filepath.Join(preferred, "held"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := freeBackupPath(exe)
	if got == preferred || !strings.HasPrefix(got, preferred+".") {
		t.Fatalf("fallback backup path = %q, want a unique sibling of %q", got, preferred)
	}

	// Cleanup collects the fallback name too, or it accumulates across updates.
	if err := os.WriteFile(got, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}
	CleanupAfterUpdate(t.TempDir(), exe, "v1.0.0")
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Fatalf("uniquely named backup survived cleanup: %v", err)
	}
}
