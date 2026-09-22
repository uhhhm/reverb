//go:build linux && !desktop

package main

import (
	"fmt"
	"os/exec"
)

// showStartupErrorDialog is the non-Wails fallback. Production desktop builds
// use the GTK implementation, which has no optional helper-program dependency.
func showStartupErrorDialog(title, message string) error {
	commands := [][]string{
		{"zenity", "--error", "--title=" + title, "--text=" + message},
		{"kdialog", "--title", title, "--error", message},
		{"xmessage", "-center", message},
	}
	var last error
	for _, command := range commands {
		path, err := exec.LookPath(command[0])
		if err != nil {
			last = err
			continue
		}
		if err := exec.Command(path, command[1:]...).Run(); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return fmt.Errorf("no desktop dialog could be shown: %w", last)
}
