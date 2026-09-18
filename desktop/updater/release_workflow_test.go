package updater

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The release workflow and this package agree on two strings and nothing else:
// the name of the asset attached to the release, and the name of the entry
// inside it. Neither side can see the other, and a rename on the workflow side
// produces a release whose assets no installed build will ever pick -- silently,
// because "no build for this platform yet" is a state the service is designed
// to wait through. These tests read the workflow and hold it to what the
// updater asks for.

func workflowSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "desktop.yml"))
	if err != nil {
		t.Fatalf("reading the release workflow: %v", err)
	}
	return string(b)
}

// artifactNames expands every artifact="..." assignment in the workflow for tag,
// resolving the shell and matrix substitutions the runner would.
func artifactNames(t *testing.T, source, tag string) map[string]string {
	t.Helper()
	// The matrix rows the bash job is run once per, as goos/arch pairs.
	matrix := regexp.MustCompile(`goos:\s*(\w+)\s*\n\s*arch:\s*(\w+)`).FindAllStringSubmatch(source, -1)
	if len(matrix) == 0 {
		t.Fatal("no build matrix rows found; the workflow's shape changed")
	}
	names := map[string]string{}
	for _, m := range regexp.MustCompile(`artifact="([^"]+)"`).FindAllStringSubmatch(source, -1) {
		raw := strings.ReplaceAll(m[1], "${TAG#v}", strings.TrimPrefix(tag, "v"))
		if strings.Contains(raw, "${TARGET_OS}") {
			for _, row := range matrix {
				n := strings.ReplaceAll(raw, "${TARGET_OS}", row[1])
				n = strings.ReplaceAll(n, "${TARGET_ARCH}", row[2])
				names[row[1]+"/"+row[2]] = n
			}
			continue
		}
		// The Windows job spells its platform out rather than taking it from a
		// matrix, so recover the pair from the name itself.
		fields := strings.Split(strings.TrimSuffix(raw, ".zip"), "-")
		if len(fields) < 2 {
			t.Fatalf("cannot read a platform out of artifact name %q", raw)
		}
		names[fields[len(fields)-2]+"/"+fields[len(fields)-1]] = raw
	}
	return names
}

// Every asset the workflow publishes is one an installed build of that platform
// will select.
func TestReleaseWorkflowAssetsAreSelectable(t *testing.T) {
	names := artifactNames(t, workflowSource(t), "v1.2.3")
	want := []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64"}
	for _, platform := range want {
		name, ok := names[platform]
		if !ok {
			t.Fatalf("the workflow publishes nothing for %s; found %v", platform, names)
		}
		goos, goarch, _ := strings.Cut(platform, "/")
		rel := &Release{Tag: "v1.2.3", Assets: []Asset{
			{Name: "reverb-desktop-1.2.3-notes.txt", URL: "https://example.com/notes"},
			{Name: name, URL: "https://example.com/asset"},
		}}
		got := PickAsset(rel, goos, goarch)
		if got == nil {
			t.Fatalf("PickAsset(%s) found nothing in the release; %q is not a name the updater recognises", platform, name)
		}
		if got.Name != name {
			t.Fatalf("PickAsset(%s) = %q, want %q", platform, got.Name, name)
		}
	}
	if len(names) != len(want) {
		t.Fatalf("the workflow publishes %d assets, the updater knows %d: %v", len(names), len(want), names)
	}
}

// The zip holds the executable under the exact name the updater unpacks by.
// The workflow zips dist/<file>, so the entry is that file's base name.
func TestReleaseWorkflowPackagesTheExpectedEntry(t *testing.T) {
	source := workflowSource(t)
	for _, tc := range []struct {
		goos, zipped string
	}{
		{"linux", "(cd dist && zip \"$artifact\" reverb-desktop)"},
		{"darwin", "(cd dist && zip \"$artifact\" reverb-desktop)"},
		{"windows", "Compress-Archive -Path dist/reverb-desktop.exe"},
	} {
		if !strings.Contains(source, tc.zipped) {
			t.Fatalf("the %s job no longer packages %q; the entry the updater unpacks by (%q) may have changed",
				tc.goos, tc.zipped, payloadName(tc.goos))
		}
		// The packaged path ends in the name unzipNamed asks for.
		if !strings.Contains(tc.zipped, payloadName(tc.goos)) {
			t.Fatalf("the %s job packages something other than %q", tc.goos, payloadName(tc.goos))
		}
	}
	// The Windows payload is additionally verified from the bytes in CI; that
	// check has to keep naming the same entry.
	if !strings.Contains(source, "verify-windows-artifact -zip") {
		t.Fatal("the Windows job no longer verifies the release zip it uploads")
	}
}

// A zip whose entry is named for another platform is refused rather than
// installed: the payload becomes the running executable, so an archive that
// does not carry this platform's binary must fail loudly.
func TestUnzipRefusesAnotherPlatformsPayload(t *testing.T) {
	dir := t.TempDir()
	want := payloadName(runtime.GOOS)
	archive := filepath.Join(dir, "release.zip")
	if err := os.WriteFile(archive, zipOf(t, want+"-someotherplatform", fakeBinary("x")), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := unzipNamed(archive, dir, want)
	if err == nil {
		t.Fatal("unzipNamed accepted an archive with no payload for this platform")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not name the entry that was missing", err)
	}
}
