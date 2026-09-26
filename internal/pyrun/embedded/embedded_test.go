//go:build pyembed

package embedded

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Stand-in modules, each run as `python -m name`.
var modules = map[string]string{
	"echoer": `import sys
print("args:" + "|".join(sys.argv[1:]))
sys.stdout.write("[download]  10%\r[download]  55%\r")
sys.stdout.flush()
print("to stderr", file=sys.stderr)
print("done")
`,
	"failer":   "import sys\nprint('boom')\nsys.exit(3)\n",
	"sleeper":  "import time\nprint('started', flush=True)\ntime.sleep(60)\n",
	"longline": "import sys\nsys.stdout.write('x' * (2 << 20) + '\\n')\nprint('after')\n",
	// Prints its argument, with a thread it starts, a few times over.
	"chatter": `import sys, threading, time
me = sys.argv[1]
def child():
    for _ in range(20):
        print("child " + me)
        time.sleep(0.001)
t = threading.Thread(target=child)
t.start()
for _ in range(20):
    print("main " + sys.argv[1])
    time.sleep(0.001)
t.join()
`,
	// Runs a native tool the way yt-dlp does, and prints what it said.
	"tool": `import subprocess, sys
p = subprocess.run(sys.argv[1:], capture_output=True, text=True)
sys.stdout.write(p.stdout)
sys.stdout.write(p.stderr)
print("status", p.returncode)
sys.exit(1 if p.returncode else 0)
`,
	// A tool whose output is inherited rather than captured.
	"inherit": `import subprocess, sys
print("status", subprocess.call(sys.argv[1:]))
`,
}

var (
	testRunner *Runner
	modulesDir string
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pyembed")
	if err != nil {
		panic(err)
	}
	modulesDir = filepath.Join(dir, "modules")
	for name, main := range modules {
		pkg := filepath.Join(modulesDir, name)
		must(os.MkdirAll(pkg, 0o755))
		must(os.WriteFile(filepath.Join(pkg, "__init__.py"), nil, 0o644))
		must(os.WriteFile(filepath.Join(pkg, "__main__.py"), []byte(main), 0o644))
	}
	paths := []string{modulesDir}
	// The desktop's bundled tools, when built, exercise the real yt-dlp.
	if site := os.Getenv("REVERB_TEST_SITE_PACKAGES"); site != "" {
		paths = append(paths, site)
	}
	testRunner, err = Start(Config{
		Home:       os.Getenv("REVERB_PYEMBED_HOME"),
		Paths:      paths,
		ToolsDir:   filepath.Join(dir, "tools"),
		SpotDLHome: filepath.Join(dir, "config"),
	})
	if err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func runLines(t *testing.T, module string, args ...string) ([]string, error) {
	t.Helper()
	var lines []string
	err := testRunner.RunModule(context.Background(), module, args, func(l string) { lines = append(lines, l) })
	return lines, err
}

func TestRunsAModuleAndStreamsItsLines(t *testing.T) {
	lines, err := runLines(t, "echoer", "--flag", "a b")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"args:--flag|a b", "[download]  10%", "[download]  55%", "to stderr", "done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q is missing %q", got, want)
		}
	}
}

func TestReportsAFailingModule(t *testing.T) {
	lines, err := runLines(t, "failer")
	if err == nil {
		t.Fatal("a module that exited 3 reported success")
	}
	if len(lines) != 1 || lines[0] != "boom" {
		t.Fatalf("lines = %q", lines)
	}
	if _, err := runLines(t, "no_such_module_here"); err == nil {
		t.Fatal("a missing module reported success")
	}
}

func TestStopsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	err := testRunner.RunModule(ctx, "sleeper", nil, func(l string) {
		if l == "started" {
			cancel()
		}
	})
	if err == nil {
		t.Fatal("a cancelled run reported success")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("cancellation took %s", time.Since(start))
	}
	// The interpreter is still usable afterwards.
	if _, err := runLines(t, "echoer"); err != nil {
		t.Fatalf("a run after a cancel: %v", err)
	}
}

func TestReturnsAfterAnOverlongLine(t *testing.T) {
	done := make(chan error, 1)
	go func() { _, err := runLines(t, "longline"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an overlong line was reported as a clean run")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunModule hung on an overlong line")
	}
}

// Concurrent runs each see their own argv, and their output, including their
// threads' output, reaches only them.
func TestConcurrentRunsKeepTheirOwnArgvAndOutput(t *testing.T) {
	var wg sync.WaitGroup
	names := []string{"alpha", "bravo", "charlie", "delta"}
	outs := make([][]string, len(names))
	errs := make([]error, len(names))
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], errs[i] = runLines(t, "chatter", name)
		}()
	}
	wg.Wait()
	for i, name := range names {
		if errs[i] != nil {
			t.Fatalf("%s: %v", name, errs[i])
		}
		if len(outs[i]) != 40 {
			t.Fatalf("%s printed %d lines: %q", name, len(outs[i]), outs[i])
		}
		for _, l := range outs[i] {
			if !strings.HasSuffix(l, " "+name) {
				t.Fatalf("%s received %q", name, l)
			}
		}
	}
}

func TestQuickJSRunsAScriptInProcess(t *testing.T) {
	script := filepath.Join(t.TempDir(), "s.js")
	must(os.WriteFile(script, []byte(`const r = [1, 2, 3].map(x => x * 2);
Promise.resolve(r).then(v => console.log(JSON.stringify({ doubled: v })));
`), 0o644))
	lines, err := runLines(t, "tool", "qjs", "--script", script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "\n") != "{\"doubled\":[2,4,6]}\nstatus 0" {
		t.Fatalf("lines = %q", lines)
	}

	lines, err = runLines(t, "tool", "/placeholder/qjs", "--help")
	if err != nil || len(lines) < 1 || !strings.HasPrefix(lines[0], "QuickJS-ng version ") {
		t.Fatalf("qjs --help: %q, %v", lines, err)
	}

	bad := filepath.Join(t.TempDir(), "bad.js")
	must(os.WriteFile(bad, []byte("throw new Error('nope')"), 0o644))
	lines, _ = runLines(t, "tool", "qjs", "--script", bad)
	if got := strings.Join(lines, "\n"); !strings.Contains(got, "nope") || !strings.HasSuffix(got, "status 1") {
		t.Fatalf("a throwing script: %q", got)
	}
}

// writeWAV writes seconds of silence for ffmpeg to read.
func writeWAV(t *testing.T, path string, seconds int) {
	t.Helper()
	cmd := exec.Command("python3", "-c", `import sys, wave
w = wave.open(sys.argv[1], "wb")
w.setnchannels(1); w.setsampwidth(2); w.setframerate(8000)
w.writeframes(b"\0\0" * 8000 * int(sys.argv[2]))
w.close()`, path, strconv.Itoa(seconds))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write wav: %v %s", err, out)
	}
}

func probeDuration(t *testing.T, path string) float64 {
	t.Helper()
	lines, err := runLines(t, "tool", "ffprobe", "-v", "error", "-show_format", "-of", "json", path)
	if err != nil {
		t.Fatalf("ffprobe: %v %q", err, lines)
	}
	var out struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	body := strings.Join(lines[:len(lines)-1], "\n")
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("ffprobe output %q: %v", body, err)
	}
	var d float64
	if err := json.Unmarshal([]byte(out.Format.Duration), &d); err != nil {
		t.Fatalf("duration %q: %v", out.Format.Duration, err)
	}
	return d
}

// ffmpeg and ffprobe run in-process, repeatedly, each run as if it were a
// fresh process: options from an earlier run do not carry over.
func TestFFmpegAndFFprobeRunInProcessRepeatedly(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.wav")
	writeWAV(t, in, 3)

	lines, err := runLines(t, "tool", "ffmpeg", "-version")
	if err != nil || len(lines) == 0 || !strings.HasPrefix(lines[0], "ffmpeg version ") {
		t.Fatalf("ffmpeg -version: %q, %v", lines, err)
	}

	first := filepath.Join(dir, "first.wav")
	if lines, err := runLines(t, "tool", "ffmpeg", "-y", "-i", in, "-c", "copy", "-t", "1", first); err != nil || lines[len(lines)-1] != "status 0" {
		t.Fatalf("first ffmpeg: %v %q", err, lines)
	}
	second := filepath.Join(dir, "second.wav")
	if lines, err := runLines(t, "tool", "ffmpeg", "-i", in, "-c", "copy", second); err != nil || lines[len(lines)-1] != "status 0" {
		t.Fatalf("second ffmpeg: %v %q", err, lines)
	}
	if d := probeDuration(t, first); d < 0.9 || d > 1.1 {
		t.Fatalf("first is %.2fs, want 1s", d)
	}
	if d := probeDuration(t, second); d < 2.9 || d > 3.1 {
		t.Fatalf("second is %.2fs, want 3s: -t leaked from the first run", d)
	}
	// Without -y, the existing file is not overwritten: -y did not leak either.
	// (ffmpeg declines with status 0.)
	lines, _ = runLines(t, "tool", "ffmpeg", "-i", in, "-c", "copy", second)
	if !strings.Contains(strings.Join(lines, "\n"), "already exists. Exiting.") {
		t.Fatalf("ffmpeg overwrote a file without -y: %q", lines)
	}
	// Output a tool's caller did not capture is the run's own output.
	lines, err = runLines(t, "inherit", "ffmpeg", "-hide_banner", "-i", in, "-f", "null", "-")
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "Input #0, wav") {
		t.Fatalf("inherited ffmpeg output: %v %q", err, lines)
	}
}

func TestCancellingARunStopsItsFFmpeg(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "long.wav")
	writeWAV(t, in, 60)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Second, cancel)
	start := time.Now()
	// -re reads at the input's own rate: a minute, unless stopped.
	var lines []string
	err := testRunner.RunModule(ctx, "tool", []string{"ffmpeg", "-re", "-i", in, "-c", "copy", "-f", "null", "-"}, func(l string) { lines = append(lines, l) })
	if err == nil {
		t.Fatalf("a cancelled ffmpeg run reported success: %q", lines)
	}
	if took := time.Since(start); took < 900*time.Millisecond || took > 8*time.Second {
		t.Fatalf("ffmpeg stopped after %s, cancelled after 1s: %q", took, lines)
	}
	if lines, err := runLines(t, "tool", "ffmpeg", "-version"); err != nil || !strings.HasPrefix(lines[0], "ffmpeg version") {
		t.Fatalf("ffmpeg after a cancel: %q %v", lines, err)
	}
}

// yt-dlp finds QuickJS as its JavaScript runtime and ffmpeg as its
// post-processor, both in-process. Needs yt-dlp on REVERB_TEST_SITE_PACKAGES.
func TestYtDlpSeesQuickJSAndFFmpeg(t *testing.T) {
	if os.Getenv("REVERB_TEST_SITE_PACKAGES") == "" {
		t.Skip("REVERB_TEST_SITE_PACKAGES is not set")
	}
	lines, _ := runLines(t, "yt_dlp", "-v", "--simulate", "--no-warnings")
	got := strings.Join(lines, "\n")
	for _, want := range []string{"[debug] JS runtimes: quickjs-ng-", "[debug] exe versions: ffmpeg "} {
		if !strings.Contains(got, want) {
			t.Fatalf("yt-dlp -v is missing %q:\n%s", want, got)
		}
	}
}

// A real resolve, as external playback does it, and a solve of YouTube's
// JavaScript challenge with the in-process QuickJS. Needs the network and
// yt-dlp on REVERB_TEST_SITE_PACKAGES; set REVERB_TEST_NETWORK=1.
func TestYtDlpResolvesAndSolvesChallengesWithQuickJS(t *testing.T) {
	if os.Getenv("REVERB_TEST_SITE_PACKAGES") == "" || os.Getenv("REVERB_TEST_NETWORK") == "" {
		t.Skip("set REVERB_TEST_SITE_PACKAGES and REVERB_TEST_NETWORK=1")
	}
	const video = "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	lines, err := runLines(t, "yt_dlp", "--no-warnings", "--socket-timeout", "15",
		"-f", "bestaudio", "--no-playlist", "-g", video)
	got := strings.Join(lines, "\n")
	if err != nil || !strings.HasPrefix(lines[len(lines)-1], "https://") {
		t.Fatalf("resolve: %v\n%s", err, got)
	}

	// The mweb client's formats need the n challenge solved. They may still
	// be withheld for want of a PO token, which is not QuickJS's concern.
	lines, _ = runLines(t, "yt_dlp", "-v", "--socket-timeout", "15",
		"--extractor-args", "youtube:player_client=mweb", "--skip-download", video)
	got = strings.Join(lines, "\n")
	if !strings.Contains(got, "[jsc:quickjs] Solving JS challenges using quickjs") {
		t.Fatalf("QuickJS was not used:\n%s", got)
	}
	for _, bad := range []string{"challenge solving failed", "Error running QuickJS"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q:\n%s", bad, got)
		}
	}
}

// The runner says which modules are installed without importing them, so the
// phone offers only the downloaders it can run.
func TestHasModule(t *testing.T) {
	if !testRunner.HasModule("echoer") {
		t.Error("an installed module is reported missing")
	}
	if testRunner.HasModule("no_such_module_here") {
		t.Error("a missing module is reported installed")
	}
}

// Activation must replace cached imports, restore them after a failed import,
// preserve QuickJS defaults, and wait for running Python (even after its Go
// caller has cancelled). Exercise the public Runner interface.
func TestYtDlpActivationAndRollback(t *testing.T) {
	if os.Getenv("REVERB_TEST_SITE_PACKAGES") == "" {
		t.Skip("bundled yt-dlp required")
	}
	ctx := context.Background()
	version := func() string {
		lines, err := runLines(t, "yt_dlp", "--version")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	original := version()
	bundle := os.Getenv("REVERB_TEST_SITE_PACKAGES")
	if err := testRunner.ActivateYtDlp(ctx, bundle, original); err != nil {
		t.Fatal(err)
	}
	broken := t.TempDir()
	must(os.MkdirAll(filepath.Join(broken, "yt_dlp"), 0700))
	must(os.WriteFile(filepath.Join(broken, "yt_dlp", "__init__.py"), []byte("raise RuntimeError('broken package')\n"), 0600))
	if err := testRunner.ActivateYtDlp(ctx, broken, "bad"); err == nil {
		t.Fatal("activated broken package")
	}
	if got := version(); got != original {
		t.Fatalf("rollback version = %q, want %q", got, original)
	}
	if err := testRunner.ActivateYtDlp(ctx, bundle, "wrong version"); err == nil {
		t.Fatal("accepted mismatching version")
	}
	if got := version(); got != original {
		t.Fatalf("version after mismatch = %q", got)
	}
}
