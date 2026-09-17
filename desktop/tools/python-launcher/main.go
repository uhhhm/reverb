//go:build windows

// Command python-launcher is built twice by setup-python-venv.sh: once as
// spotdl.exe and once as yt-dlp.exe. It keeps the bundled Python invocation
// relocatable; pip-generated Windows launchers embed the absolute path at which
// pip ran, which is not where an installed Reverb bundle will live.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

var moduleName string

func main() {
	if moduleName == "" {
		fmt.Fprintln(os.Stderr, "python launcher: module name was not set at build time")
		os.Exit(2)
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	python := filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "python", "python.exe"))
	args := append([]string{"-m", moduleName}, os.Args[1:]...)
	cmd := exec.Command(python, args...)
	cmd.Env = os.Environ()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
