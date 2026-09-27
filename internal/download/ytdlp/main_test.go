package ytdlp

import (
	"os"
	"testing"
)

// TestMain points os.UserConfigDir at a temp dir for the whole package: Init
// writes the cookies file when cookies are configured, and a test must never
// overwrite the developer's real one.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ytdlp-config")
	if err != nil {
		panic(err)
	}
	for _, k := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
		os.Setenv(k, dir)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
