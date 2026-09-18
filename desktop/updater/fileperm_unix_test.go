//go:build !windows

package updater

import (
	"os"
	"testing"
)

// assertRunnable checks that an installed binary can be executed. On unix that
// is the execute bit, which a copy that dropped the mode would lose.
func assertRunnable(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("installed binary is missing: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: %v", fi.Mode())
	}
}
