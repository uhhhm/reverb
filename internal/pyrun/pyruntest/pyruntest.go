// Package pyruntest runs the Python runner against stand-in modules, so code
// that drives yt-dlp or spotDL through pyrun is tested on the real host
// interpreter without the real tools or the network.
package pyruntest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/uhhhm/reverb/internal/pyrun"
)

// Host returns a host runner whose module path starts with a directory holding
// one package per entry of modules, each run as `python -m name` by the given
// __main__ source. Those packages shadow anything installed under the same
// name. It skips the test when python3 is not installed.
func Host(t testing.TB, modules map[string]string) pyrun.Host {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	dir := t.TempDir()
	for name, main := range modules {
		pkg := filepath.Join(dir, name)
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "__init__.py"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "__main__.py"), []byte(main), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return pyrun.Host{Python: py, Env: []string{"PYTHONPATH=" + dir}}
}

// ArgLog is Python that appends the module's argv, NUL-separated, as one line
// to the file named by the REVERB_TEST_ARGLOG environment variable, so a test
// can read back every invocation.
const ArgLog = `import os, sys
_log = os.environ.get("REVERB_TEST_ARGLOG")
if _log:
    with open(_log, "a") as f:
        f.write("\x00".join(sys.argv[1:]) + "\n")
`
