package extstream

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
)

// ExecRunner is the production Runner. Canceling ctx kills the child process.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, onLine func(string)) error {
	cmd := exec.CommandContext(ctx, name, args...)
	var buf bytes.Buffer
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = io.MultiWriter(pw, &buf)
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return err
	}
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.CloseWithError(err)
		waitErr <- err
	}()
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		onLine(sc.Text())
	}
	if werr := <-waitErr; werr != nil {
		return werr
	}
	return sc.Err()
}
