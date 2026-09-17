//go:build windows

package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/uhhhm/reverb/internal/childproc"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: windows_terminate PID")
		os.Exit(2)
	}
	pid, err := strconv.Atoi(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := childproc.Terminate(pid); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
