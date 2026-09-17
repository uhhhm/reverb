package updater

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/uhhhm/reverb/internal/childproc"
)

// macAppBundle returns the .app directory exePath lives in, or "" when the
// binary is not inside a bundle.
func macAppBundle(exePath string) string {
	// <bundle>.app/Contents/MacOS/<binary>
	dir := filepath.Dir(exePath)
	if filepath.Base(dir) != "MacOS" {
		return ""
	}
	contents := filepath.Dir(dir)
	if filepath.Base(contents) != "Contents" {
		return ""
	}
	bundle := filepath.Dir(contents)
	if !strings.HasSuffix(bundle, ".app") {
		return ""
	}
	return bundle
}

// backupPath keeps the rollback copy outside the .app, because its resources
// are sealed by codesign and deleting a file inside it after a successful boot
// would invalidate that seal.
func backupPath(exePath string) string {
	if bundle := macAppBundle(exePath); bundle != "" {
		return filepath.Join(filepath.Dir(bundle), "."+filepath.Base(bundle)+backupSuffix)
	}
	return exePath + backupSuffix
}

// resealInstalled re-establishes whatever the OS requires before it will run
// the replaced binary. Reverb's distributed bundles are ad-hoc signed
// (package-mac.sh), and macOS refuses to launch a bundle whose main executable
// no longer matches its signature, so the bundle is re-signed in place with its
// bundled tools intact. A failure here is fatal to the install and triggers
// rollback: an unsigned bundle will not start at all.
func resealInstalled(exePath string) error {
	if bundle := macAppBundle(exePath); bundle != "" {
		if output, err := childproc.Command("/usr/bin/codesign", "--force", "--sign", "-", bundle).CombinedOutput(); err != nil {
			return fmt.Errorf("sign updated app: %w: %s", err, output)
		}
	}
	return nil
}

// relaunchCommand launches the bundle rather than the executable, which keeps
// the Dock icon, the app name and the activation behaviour macOS attaches to
// it. open exits as soon as Launch Services has the app, so its exit status is
// worth waiting for: an invalid bundle then triggers rollback instead of a
// silent quit into nothing.
func relaunchCommand(exePath string, args []string) (cmd *exec.Cmd, waitForExit bool) {
	if bundle := macAppBundle(exePath); bundle != "" {
		return childproc.Command("open", append([]string{"-n", bundle, "--args"}, args...)...), true
	}
	return childproc.Command(exePath, args...), false
}
