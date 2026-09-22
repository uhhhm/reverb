//go:build darwin

package main

import (
	"fmt"
	"os/exec"
	"strconv"
)

// showStartupErrorDialog implements startupErrorDialog for macOS.
func showStartupErrorDialog(title, message string) error {
	script := fmt.Sprintf("display alert %s message %s as critical", strconv.Quote(title), strconv.Quote(message))
	return exec.Command("/usr/bin/osascript", "-e", script).Run()
}
