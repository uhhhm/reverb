package updater

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFailedRelaunchRestoresWorkingExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(t.TempDir(), "reverb-desktop")
	old := fakeBinary("original")
	if err := os.WriteFile(exe, old, 0o755); err != nil {
		t.Fatal(err)
	}
	// Plausible header, but the kernel will reject this as an executable.
	useServer(t, releaseServer(t, "v2.0.0", fakeBinary("invalid update")))
	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dir, ExePath: exe})
	svc.CheckNow(context.Background())
	if err := svc.InstallAndRestart(); err == nil {
		t.Fatal("expected launch failure")
	}
	got, err := os.ReadFile(exe)
	if err != nil || !bytes.Equal(got, old) {
		t.Fatalf("working executable was not restored: %v", err)
	}
}

func TestVersionPrecedenceIgnoresBuildMetadata(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		want            bool
	}{
		{"v1.0.0+first", "v1.0.0+second", false},
		{"v1.0.0-rc.9", "v1.0.0-rc.10", true},
		{"v1.0.0", "not-a-release", false},
	} {
		if got := IsNewer(tc.current, tc.latest); got != tc.want {
			t.Errorf("%s -> %s: %v", tc.current, tc.latest, got)
		}
	}
}

func TestUpdaterSelectsInstallableLinuxAsset(t *testing.T) {
	rel := &Release{Assets: []Asset{
		{Name: "reverb-desktop-2.0.0-linux-amd64.deb"},
		{Name: "reverb-desktop-2.0.0-linux-amd64.zip"},
	}}
	a := PickAsset(rel, "linux", "amd64")
	if a == nil || a.Name != rel.Assets[1].Name {
		t.Fatalf("selected %v; want executable zip", a)
	}
}

func TestFailedNewDownloadKeepsPreviouslyStagedUpdate(t *testing.T) {
	dir := t.TempDir()
	useServer(t, releaseServer(t, "v2.0.0", fakeBinary("v2")))
	svc := New(Options{Repo: "owner/name", CurrentVersion: "v1.0.0", DataDir: dir})
	if st := svc.CheckNow(context.Background()); st.Staged != "v2.0.0" {
		t.Fatal(st)
	}
	useServer(t, releaseServer(t, "v3.0.0", []byte("broken")))
	svc.CheckNow(context.Background())
	su, ok := ReadStaged(dir)
	if !ok || su.Tag != "v2.0.0" {
		t.Fatalf("working staged update was lost: %+v, valid=%v", su, ok)
	}
}
