// Package pyrun runs Python tools — yt-dlp and spotDL — as modules rather than
// as bundled executables. A phone cannot spawn the desktop's executables
// (ADR 0003), so the downloader adapters and external stream resolution take a
// Runner, and each platform supplies one: an embedded interpreter on iOS, and
// the host's own Python on Linux, which is what lets the phone profile run and
// be tested before any iOS work.
package pyrun

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"

	"github.com/uhhhm/reverb/internal/childproc"
)

// Module names of the tools Reverb runs.
const (
	YtDlp  = "yt_dlp"
	SpotDL = "spotdl"
)

// DefaultPython is the host interpreter used when REVERB_PYTHON is unset.
const DefaultPython = "python3"

// Runner runs a Python module's command line, as `python -m module args` would,
// and streams its combined output line by line. A carriage return ends a line
// too, since both tools redraw their progress line with one. It returns once
// the module has finished; a non-zero exit is an error. Cancelling ctx stops
// the module.
type Runner interface {
	RunModule(ctx context.Context, module string, args []string, onLine func(string)) error
}

// Host runs modules with an interpreter installed on this machine.
type Host struct {
	// Python is the interpreter to run.
	Python string
	// Env is added to this process's environment, e.g. a PYTHONPATH.
	Env []string
}

// HostFromEnv is the host interpreter named by REVERB_PYTHON, or python3.
func HostFromEnv(getenv func(string) string) Host {
	py := getenv("REVERB_PYTHON")
	if py == "" {
		py = DefaultPython
	}
	return Host{Python: py}
}

func (h Host) RunModule(ctx context.Context, module string, args []string, onLine func(string)) error {
	cmd := childproc.CommandContext(ctx, h.Python, append([]string{"-m", module}, args...)...)
	// Unbuffered, or progress arrives in one lump when the module exits.
	cmd.Env = append(append(os.Environ(), "PYTHONUNBUFFERED=1"), h.Env...)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return err
	}
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.CloseWithError(err)
		waitErr <- err
	}()
	scanErr := ScanLines(pr, onLine)
	if err := <-waitErr; err != nil {
		return err
	}
	return scanErr
}

// ScanLines calls onLine for each line r yields, ending a line at '\n' or
// '\r', until r is exhausted. A line past the scanner's buffer stops the
// calls, but r is still drained, so a writer blocked on it can finish.
func ScanLines(r io.Reader, onLine func(string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sc.Split(scanLinesCR)
	for sc.Scan() {
		onLine(sc.Text())
	}
	_, _ = io.Copy(io.Discard, r)
	return sc.Err()
}

// ModuleRunner runs one module where a caller expects an executable: it has
// the Run shape the yt-dlp and spotDL adapters and extstream take, and ignores
// the binary name they pass.
type ModuleRunner struct {
	runner Runner
	module string
}

// Module adapts r to run module in place of an executable.
func Module(r Runner, module string) ModuleRunner {
	return ModuleRunner{runner: r, module: module}
}

func (m ModuleRunner) Run(ctx context.Context, _ string, args []string, onLine func(string)) error {
	return m.runner.RunModule(ctx, m.module, args, onLine)
}

// scanLinesCR splits on either '\n' or '\r'.
func scanLinesCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
