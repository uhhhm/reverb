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

// bundledToolDirs lists, in priority order, the directories a bundled tool may
// live in: beside the running executable first (an installed app), then beside
// argv[0], then the working tree's desktop/tools, so a developer build finds
// the tools it just downloaded.
func bundledToolDirs(name string) []string {
	var dirs []string
	venv := inPythonVenv(name)

	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		dirs = append(dirs,
			filepath.Join(dir, "../Resources/bin"),
			filepath.Join(dir, "Resources/bin"),
			filepath.Join(dir, "bin"),
		)
		if venv {
			dirs = append(dirs,
				filepath.Join(dir, "../Resources/python", pythonBinSubdir),
				filepath.Join(dir, "python", pythonBinSubdir),
			)
		}
	}

	argv0 := filepath.Dir(os.Args[0])
	dirs = append(dirs,
		filepath.Join(argv0, "../Resources/bin"),
		filepath.Join(argv0, "Resources/bin"),
	)
	if venv {
		dirs = append(dirs, filepath.Join(argv0, "../Resources/python", pythonBinSubdir))
	}

	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs,
			filepath.Join(wd, "desktop/tools/bin"),
			filepath.Join(wd, "../desktop/tools/bin"),
			filepath.Join(wd, "../../desktop/tools/bin"),
			filepath.Join(wd, "./desktop/tools/bin"),
		)
		if venv {
			dirs = append(dirs,
				filepath.Join(wd, "desktop/tools/python", pythonBinSubdir),
				filepath.Join(wd, "../desktop/tools/python", pythonBinSubdir),
				filepath.Join(wd, "../../desktop/tools/python", pythonBinSubdir),
			)
		}
	}

	dirs = append(dirs, "desktop/tools/bin", "./desktop/tools/bin")
	if venv {
		dirs = append(dirs,
			filepath.Join("desktop/tools/python", pythonBinSubdir),
			filepath.Join("./desktop/tools/python", pythonBinSubdir),
		)
	}

	return dirs
}

// findBundledTool returns the absolute path of the first runnable copy of name,
// or "" when none is found. The file name a tool is installed under is an OS
// detail (toolFileNames), as is what makes a file runnable (isExecutable).
func findBundledTool(name string) string {
	for _, dir := range bundledToolDirs(name) {
		for _, file := range toolFileNames(name) {
			c := filepath.Join(dir, file)
			if abs, err := filepath.Abs(c); err == nil {
				c = abs
			}
			c = filepath.Clean(c)
			if isExecutable(c) {
				return c
			}
		}
	}

	// Nothing bundled: fall back to whatever the household has installed.
	// LookPath applies the OS's own rules, including executable extensions.
	if p, err := exec.LookPath(name); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return p
	}

	return ""
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
