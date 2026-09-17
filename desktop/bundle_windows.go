//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
)

// pythonBinSubdir is where a Python virtual environment puts console scripts on
// Windows. The layout differs from unix by name only: spotdl and yt-dlp land
// here instead of in bin/.
const pythonBinSubdir = "Scripts"

// toolFileNames returns the file names a tool may be installed under, most
// specific first. Windows identifies an executable by its extension, and the
// bundled tools arrive as .exe while a venv console script may be a .exe shim
// or a .cmd/.bat wrapper. A file staged without an extension is deliberately
// not a candidate: Windows would not run it, so finding it would only turn a
// missing tool into a failure at spawn time.
func toolFileNames(name string) []string {
	if filepath.Ext(name) != "" {
		return []string{name}
	}
	return []string{name + ".exe", name + ".cmd", name + ".bat"}
}

// isExecutable reports whether path is a file this process could run. Windows
// has no execute permission bit; what makes a file runnable is its extension
// appearing in PATHEXT, so that is what is checked.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	for _, e := range strings.Split(pathext, ";") {
		if strings.EqualFold(strings.TrimSpace(e), ext) {
			return true
		}
	}
	return false
}
