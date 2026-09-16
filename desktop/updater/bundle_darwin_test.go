package updater

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMacBundleRemainsValidAfterUpdateAndCleanup(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Reverb.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "reverb-desktop")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	info := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>app.reverb.test</string><key>CFBundleExecutable</key><string>reverb-desktop</string><key>CFBundlePackageType</key><string>APPL</string></dict></plist>`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(info), 0o644); err != nil {
		t.Fatal(err)
	}
	buildProbe(t, "v1", exe)
	if err := refreshBundleSignature(exe); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	payload := filepath.Join(dir, "v2")
	buildProbe(t, "v2", payload)
	sum, err := FileSHA256(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteStaged(dir, StagedUpdate{Tag: "v2.0.0", File: payload, SHA256: sum}); err != nil {
		t.Fatal(err)
	}
	if err := ApplyStaged(dir, exe); err != nil {
		t.Fatal(err)
	}
	verify := func() {
		t.Helper()
		if out, err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", bundle).CombinedOutput(); err != nil {
			t.Fatalf("bundle signature: %v: %s", err, out)
		}
	}
	verify()
	CleanupAfterUpdate(dir, exe, "v2.0.0")
	verify()
}
