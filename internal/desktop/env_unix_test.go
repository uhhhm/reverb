//go:build !windows

package desktop

import "testing"

// setConfigHome and setHome redirect the directories os.UserConfigDir and
// os.UserHomeDir resolve. Which variables carry them is per-OS, and the tests
// are about Reverb's layout within those directories rather than about how the
// host names them. Passing "" makes the lookup fail, which is how the
// fallback paths are reached.
func setConfigHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", dir)
}

func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
}
