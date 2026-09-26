//go:build ios && pyembed

package reverbcore

import (
	"context"
	"errors"
	"github.com/uhhhm/reverb/internal/ytdlpupdate"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/uhhhm/reverb/internal/pyrun"
	"github.com/uhhhm/reverb/internal/pyrun/embedded"
)

// phonePython starts the app's embedded interpreter (once per process: a
// restarted core reuses it).
func phonePython(dataDir string) (pyrun.Runner, error) {
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
	phoneUpdates.Lock()
	defer phoneUpdates.Unlock()
	if phoneUpdates.updater == nil {
		var lines []string
		err := r.RunModule(context.Background(), "yt_dlp", []string{"--version"}, func(line string) { lines = append(lines, line) })
		if err != nil {
			return nil, err
		}
		phoneUpdates.bundledVersion = strings.TrimSpace(strings.Join(lines, "\n"))
		u := ytdlpupdate.New(filepath.Join(dataDir, "yt-dlp"), updateKey(), r.ActivateYtDlp)
		if err := u.Restore(context.Background()); err != nil {
			log.Printf("yt-dlp: keeping bundled package: %v", err)
		}
		phoneUpdates.updater = u
	}
	return r, nil
}
