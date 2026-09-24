//go:build ios && pyembed

package reverbcore

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/uhhhm/reverb/internal/pyrun"
	"github.com/uhhhm/reverb/internal/pyrun/embedded"
)

// phonePython starts the app's embedded interpreter (once per process: a
// restarted core reuses it).
func phonePython() (pyrun.Runner, error) {
	pythonMu.Lock()
	home, packages := pythonHome, pythonPackages
	pythonMu.Unlock()
	if home == "" {
		return nil, errors.New("reverbcore: ConfigurePython was not called")
	}
	// spotDL's config sits where the Go adapter prepares it.
	config, _ := os.UserConfigDir()
	r, err := embedded.Start(embedded.Config{
		Home:          home,
		Paths:         []string{packages},
		ToolsDir:      filepath.Join(os.TempDir(), "reverb-tools"),
		SpotDLHome:    config,
		WriteBytecode: true,
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}
