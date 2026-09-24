//go:build pyembed

package embedded

/*
#include <stdlib.h>
#include "embedded.h"
*/
import "C"

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/uhhhm/reverb/internal/pyrun"
)

//go:embed launcher.py
var launcherSource string

// cancelGrace is how long a cancelled run has to wind down before RunModule
// returns without it. Python code blocked in a call (a socket read, a sleep)
// only sees the cancel once the call returns; it then stops by itself, with
// its output going nowhere.
const cancelGrace = 2 * time.Second

// Config is where the interpreter finds Python and may write.
type Config struct {
	// Home is Python's prefix, with the standard library under
	// lib/python3.14. Empty lets the linked libpython find its own.
	Home string
	// Paths are appended to sys.path: the bundled packages.
	Paths []string
	// ToolsDir is a writable directory for the stand-ins that tell the tools
	// ffmpeg, ffprobe and qjs exist.
	ToolsDir string
	// SpotDLHome is where spotDL keeps its config and temp directory, as
	// SpotDLHome/spotdl. Empty leaves spotDL's own choice.
	SpotDLHome string
	// WriteBytecode caches compiled modules beside their sources, where that
	// is writable. The app's bundle is not, and ships compiled.
	WriteBytecode bool
}

// Runner runs modules in the process's one embedded interpreter.
type Runner struct {
	tokens atomic.Int64
}

var (
	startOnce sync.Once
	started   *Runner
	startErr  error
)

// Start starts the interpreter. A process has one: later calls return the
// first call's runner, whatever their config.
func Start(cfg Config) (*Runner, error) {
	startOnce.Do(func() {
		started, startErr = start(cfg)
	})
	return started, startErr
}

func start(cfg Config) (*Runner, error) {
	if cfg.ToolsDir == "" {
		return nil, errors.New("embedded: ToolsDir is required")
	}
	cs := func(s string) *C.char { return C.CString(s) }
	home, launcher, tools, spot := cs(cfg.Home), cs(launcherSource), cs(cfg.ToolsDir), cs(cfg.SpotDLHome)
	defer func() {
		for _, p := range []*C.char{home, launcher, tools, spot} {
			C.free(unsafe.Pointer(p))
		}
	}()
	paths, free := cStrings(cfg.Paths)
	defer free()
	var cerr *C.char
	bytecode := C.int(0)
	if cfg.WriteBytecode {
		bytecode = 1
	}
	if C.reverb_py_start(home, paths, C.int(len(cfg.Paths)), launcher, tools, spot, bytecode, &cerr) != 0 {
		defer C.free(unsafe.Pointer(cerr))
		return nil, fmt.Errorf("embedded: %s", C.GoString(cerr))
	}
	return &Runner{}, nil
}

// cStrings copies ss to C for the length of a call.
func cStrings(ss []string) (**C.char, func()) {
	if len(ss) == 0 {
		return nil, func() {}
	}
	arr := (*[1 << 20]*C.char)(C.malloc(C.size_t(len(ss)) * C.size_t(unsafe.Sizeof(uintptr(0)))))[:len(ss):len(ss)]
	for i, s := range ss {
		arr[i] = C.CString(s)
	}
	return &arr[0], func() {
		for _, p := range arr {
			C.free(unsafe.Pointer(p))
		}
		C.free(unsafe.Pointer(&arr[0]))
	}
}

type result struct {
	status int
	err    error
}

// RunModule runs module as `python -m module args` would, on this goroutine's
// thread, with its output streamed to onLine. Several run at once.
func (r *Runner) RunModule(ctx context.Context, module string, args []string, onLine func(string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	token := r.tokens.Add(1)

	// onLine is never called once RunModule has returned, even when a
	// cancelled run outlives its grace.
	var mu sync.Mutex
	stopped := false
	emit := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		if !stopped {
			onLine(line)
		}
	}
	scanned := make(chan error, 1)
	go func() {
		scanned <- pyrun.ScanLines(pr, emit)
		_ = pr.Close()
	}()

	done := make(chan result, 1)
	go func() {
		status, err := run(module, args, pw.Fd(), token)
		_ = pw.Close()
		done <- result{status, err}
	}()

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		C.reverb_py_cancel(C.longlong(token))
		select {
		case res = <-done:
		case <-time.After(cancelGrace):
			mu.Lock()
			stopped = true
			mu.Unlock()
			return ctx.Err()
		}
	}
	scanErr := <-scanned
	if err := ctx.Err(); err != nil {
		return err
	}
	if res.err != nil {
		return res.err
	}
	if res.status != 0 {
		return fmt.Errorf("%s: exit status %d", module, res.status)
	}
	return scanErr
}

func run(module string, args []string, fd uintptr, token int64) (int, error) {
	cmod := C.CString(module)
	defer C.free(unsafe.Pointer(cmod))
	cargs, free := cStrings(args)
	defer free()
	var cerr *C.char
	status := C.reverb_py_run(cmod, cargs, C.int(len(args)), C.int(fd), C.longlong(token), &cerr)
	if status < 0 {
		defer C.free(unsafe.Pointer(cerr))
		return 0, errors.New(C.GoString(cerr))
	}
	return int(status), nil
}
