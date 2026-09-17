//go:build !windows

package main

import "os"

// pythonBinSubdir is the directory a Python virtual environment puts console
// scripts in. setup-python-venv.sh installs spotdl and yt-dlp there, so a port
// that names this directory differently must change it here rather than teach
// the search about a second layout.
const pythonBinSubdir = "bin"

// toolFileNames returns the file names a tool may be installed under, most
// specific first. Unix executables carry no extension, so the tool's own name
// is the only candidate.
func toolFileNames(name string) []string { return []string{name} }

// isExecutable reports whether path is a file this process could run. The
// execute bit is a real permission on unix, so a downloaded-but-not-chmodded
// tool is correctly rejected rather than found and then failing at spawn time.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}
