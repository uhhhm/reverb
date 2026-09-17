package updater

import (
	"context"
	"log"
	"os"

	"github.com/uhhhm/reverb/internal/childproc"
)

// ExecCommand is the child-process constructor, exposed for tests to mock. It
// defaults to childproc.CommandContext so the pip run inherits the platform's
// spawn configuration.
var ExecCommand = childproc.CommandContext

// DefaultPythonBin is the fallback python binary when none is supplied.
const DefaultPythonBin = "python3"

// UpgradeYtDlp upgrades yt-dlp via `python -m pip install --upgrade yt-dlp`.
// pythonBin may be empty, in which case DefaultPythonBin or REVERB_YTDLP_PYTHON
// env is used. No restart is required.
func UpgradeYtDlp(ctx context.Context, pythonBin string) error {
	if pythonBin == "" {
		if env := os.Getenv("REVERB_YTDLP_PYTHON"); env != "" {
			pythonBin = env
		} else if env := os.Getenv("REVERB_SPOTDL_PATH"); env != "" {
			// REVERB_SPOTDL_PATH points to spotdl binary; derive python from its venv if possible,
			// fallback to python3. For now just use python3 to keep contract simple.
			pythonBin = DefaultPythonBin
		} else {
			pythonBin = DefaultPythonBin
		}
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
