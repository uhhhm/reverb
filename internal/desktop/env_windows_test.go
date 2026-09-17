//go:build windows

package desktop

import "testing"

// setConfigHome and setHome redirect the directories os.UserConfigDir and
// os.UserHomeDir resolve. On Windows those are the roaming application-data
// directory and the user profile; there is no XDG layer.
func setConfigHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("AppData", dir)
}

func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("USERPROFILE", dir)
}
