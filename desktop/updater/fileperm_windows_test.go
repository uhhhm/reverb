//go:build windows

package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertRunnable checks that an installed binary can be executed. Windows has
// no execute bit — what makes a file runnable is its extension — so the name
// is what there is to assert on.
func assertRunnable(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("installed binary is missing: %v", err)
	}
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		t.Fatalf("installed binary %q is not named as an executable", path)
	}
}
