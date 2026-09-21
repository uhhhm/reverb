package pyrun

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hostOrSkip is a Host whose module path starts with dir, so a stub package
// there shadows anything installed.
func hostOrSkip(t *testing.T, dir string) Host {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	return Host{Python: py, Env: []string{"PYTHONPATH=" + dir}}
}

// stubModule writes a package whose __main__ is body.
func stubModule(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	pkg := filepath.Join(dir, name)
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "__init__.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "__main__.py"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHostRunsAModuleAndStreamsItsLines(t *testing.T) {
	dir := stubModule(t, "echoer", `import sys
print("args:" + "|".join(sys.argv[1:]))
sys.stdout.write("[download]  10%\r[download]  55%\r")
sys.stdout.flush()
print("to stderr", file=sys.stderr)
print("done")
`)
	h := hostOrSkip(t, dir)
	var lines []string
	if err := h.RunModule(context.Background(), "echoer", []string{"--flag", "a b"}, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"args:--flag|a b", "[download]  10%", "[download]  55%", "to stderr", "done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q is missing %q", got, want)
		}
	}
}

func TestHostReportsAFailingModule(t *testing.T) {
	dir := stubModule(t, "failer", "import sys\nprint('boom')\nsys.exit(3)\n")
	h := hostOrSkip(t, dir)
	var lines []string
	err := h.RunModule(context.Background(), "failer", nil, func(l string) { lines = append(lines, l) })
	if err == nil {
		t.Fatal("a module that exited 3 reported success")
	}
	if len(lines) != 1 || lines[0] != "boom" {
		t.Fatalf("lines = %q", lines)
	}
	if err := h.RunModule(context.Background(), "no_such_module_here", nil, func(string) {}); err == nil {
		t.Fatal("a missing module reported success")
	}
}

func TestHostStopsWhenTheContextIsCancelled(t *testing.T) {
	dir := stubModule(t, "sleeper", "import time\nprint('started', flush=True)\ntime.sleep(30)\n")
	h := hostOrSkip(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	err := h.RunModule(ctx, "sleeper", nil, func(l string) {
		if l == "started" {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("a cancelled run reported success")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("cancellation took %s", time.Since(start))
	}
}

type recordingRunner struct {
	module string
	args   []string
}

func (r *recordingRunner) RunModule(_ context.Context, module string, args []string, onLine func(string)) error {
	r.module, r.args = module, args
	onLine("ok")
	return nil
}

// A module runner stands in for an executable: whatever binary name the
// adapter asks for, the module it was built for runs.
func TestModuleRunnerIgnoresTheBinaryName(t *testing.T) {
	rec := &recordingRunner{}
	m := Module(rec, YtDlp)
	var got []string
	if err := m.Run(context.Background(), "/usr/local/bin/yt-dlp", []string{"--version"}, func(l string) { got = append(got, l) }); err != nil {
		t.Fatal(err)
	}
	if rec.module != "yt_dlp" || len(rec.args) != 1 || rec.args[0] != "--version" || len(got) != 1 {
		t.Fatalf("ran %s %v, lines %v", rec.module, rec.args, got)
	}
}

func TestHostFromEnv(t *testing.T) {
	if h := HostFromEnv(func(string) string { return "" }); h.Python != DefaultPython {
		t.Fatalf("Python = %q", h.Python)
	}
	env := map[string]string{"REVERB_PYTHON": "/opt/py/bin/python3"}
	if h := HostFromEnv(func(k string) string { return env[k] }); h.Python != "/opt/py/bin/python3" {
		t.Fatalf("Python = %q", h.Python)
	}
}

// A line longer than the scanner holds must not leave the module blocked on a
// pipe nobody reads.
func TestHostReturnsAfterAnOverlongLine(t *testing.T) {
	dir := stubModule(t, "longline", "import sys\nsys.stdout.write('x' * (2 << 20) + '\\n')\nprint('after')\n")
	h := hostOrSkip(t, dir)
	done := make(chan error, 1)
	go func() { done <- h.RunModule(context.Background(), "longline", nil, func(string) {}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an overlong line was reported as a clean run")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunModule hung on an overlong line")
	}
}
