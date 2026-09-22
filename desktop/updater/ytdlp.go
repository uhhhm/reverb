package updater

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/uhhhm/reverb/internal/childproc"
)

// ExecCommand is the child-process constructor, exposed for tests to mock. It
// defaults to childproc.CommandContext so the pip run inherits the platform's
// spawn configuration.
var ExecCommand = childproc.CommandContext

// ErrNoBundledPython means no interpreter shipped with Reverb was found, so
// there is nothing the app owns to upgrade yt-dlp in.
var ErrNoBundledPython = errors.New("no bundled python to upgrade yt-dlp in")

// UpgradeYtDlp upgrades yt-dlp via `python -m pip install --upgrade yt-dlp`.
// pythonBin may be empty, in which case REVERB_YTDLP_PYTHON (which desktop
// startup points at the bundled runtime) is used. It never falls back to a
// python3 on PATH: that would modify a system installation the app does not
// own. No restart is required.
func UpgradeYtDlp(ctx context.Context, pythonBin string) error {
	if pythonBin == "" {
		pythonBin = os.Getenv("REVERB_YTDLP_PYTHON")
	}
	if pythonBin == "" {
		log.Printf("updater: skipping yt-dlp upgrade: %v", ErrNoBundledPython)
		return ErrNoBundledPython
	}
	cmd := ExecCommand(ctx, pythonBin, "-m", "pip", "install", "--upgrade", "yt-dlp")
	// Inherit env but ensure pip doesn't prompt.
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("updater: yt-dlp upgrade failed (%s): %v output=%s", pythonBin, err, string(output))
		return err
	}
	log.Printf("updater: yt-dlp upgraded via %s: %s", pythonBin, string(output))
	return nil
}
