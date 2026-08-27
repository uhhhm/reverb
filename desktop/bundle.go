package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveBundledTools checks executable-relative Resources/bin and
// desktop/tools/bin, then PATH. Returns "" for any tool not found.
// Env overrides are assumed to have been handled by the caller via
// os.Getenv before invoking this helper.
func ResolveBundledTools() (ffmpeg, navidrome, spotdl, deno, ytdlp string) {
	return findBundledTool("ffmpeg"),
		findBundledTool("navidrome"),
		findBundledTool("spotdl"),
		findBundledTool("deno"),
		findBundledTool("yt-dlp")
}

// inPythonVenv reports whether name is installed into the bundled Python venv
// (setup-python-venv.sh installs spotdl and yt-dlp there) rather than bin/.
func inPythonVenv(name string) bool { return name == "spotdl" || name == "yt-dlp" }

func findBundledTool(name string) string {
	var candidates []string

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "../Resources/bin", name),
			filepath.Join(dir, "Resources/bin", name),
			filepath.Join(dir, "bin", name),
		)
		if inPythonVenv(name) {
			candidates = append(candidates,
				filepath.Join(dir, "../Resources/python/bin", name),
				filepath.Join(dir, "python/bin", name),
			)
		}
	}

	candidates = append(candidates,
		filepath.Join(filepath.Dir(os.Args[0]), "../Resources/bin", name),
		filepath.Join(filepath.Dir(os.Args[0]), "Resources/bin", name),
	)

	if inPythonVenv(name) {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(os.Args[0]), "../Resources/python/bin", name),
		)
	}

	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "desktop/tools/bin", name),
			filepath.Join(wd, "../desktop/tools/bin", name),
			filepath.Join(wd, "../../desktop/tools/bin", name),
			filepath.Join(wd, "./desktop/tools/bin", name),
		)
		if inPythonVenv(name) {
			candidates = append(candidates,
				filepath.Join(wd, "desktop/tools/python/bin", name),
				filepath.Join(wd, "../desktop/tools/python/bin", name),
				filepath.Join(wd, "../../desktop/tools/python/bin", name),
			)
		}
	}

	candidates = append(candidates,
		filepath.Join("desktop/tools/bin", name),
		filepath.Join("./desktop/tools/bin", name),
	)
	if inPythonVenv(name) {
		candidates = append(candidates,
			filepath.Join("desktop/tools/python/bin", name),
			filepath.Join("./desktop/tools/python/bin", name),
		)
	}

	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			c = abs
		}
		c = filepath.Clean(c)
		if isExecutable(c) {
			return c
		}
	}

	if p, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return p
	}

	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	if info.Mode()&0111 == 0 {
		return false
	}
	return true
}

// ApplyBundledToolEnv points the services at the binaries shipped alongside the
// app, without overriding anything the user set explicitly. This must run before
// config.Load and wiring: the Navidrome supervisor otherwise falls back to a bare
// "navidrome" on PATH, which a desktop install does not have, so the built-in
// library never starts and every library request fails with connection refused.
//
// ffmpeg and yt-dlp are invoked BY NAME by spotDL, so their directories are
// prepended to PATH rather than passed as variables.
func ApplyBundledToolEnv() {
	ffmpeg, navidrome, spotdl, deno, ytdlp := ResolveBundledTools()
	setEnvIfUnset("REVERB_NAVIDROME_BIN", navidrome)
	setEnvIfUnset("REVERB_SPOTDL_PATH", spotdl)
	setEnvIfUnset("REVERB_YTDLP_PATH", ytdlp)
	setEnvIfUnset("REVERB_DENO_PATH", deno)
	prependToPath(ffmpeg, ytdlp)
}

func setEnvIfUnset(key, value string) {
	if value == "" || os.Getenv(key) != "" {
		return
	}
	_ = os.Setenv(key, value)
}

// prependToPath adds each tool's containing directory to the front of PATH,
// skipping empties and directories already present.
func prependToPath(tools ...string) {
	path := os.Getenv("PATH")
	existing := make(map[string]bool)
	for _, d := range filepath.SplitList(path) {
		existing[d] = true
	}
	var prefix []string
	for _, t := range tools {
		if t == "" {
			continue
		}
		d := filepath.Dir(t)
		if existing[d] {
			continue
		}
		existing[d] = true
		prefix = append(prefix, d)
	}
	if len(prefix) == 0 {
		return
	}
	if path != "" {
		prefix = append(prefix, path)
	}
	_ = os.Setenv("PATH", strings.Join(prefix, string(filepath.ListSeparator)))
}
