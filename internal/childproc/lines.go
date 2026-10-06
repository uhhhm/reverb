package childproc

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os/exec"
)

// LineRunner runs an executable and streams its combined output line by line.
// It is the production Runner of the yt-dlp and spotDL adapters and extstream.
// Canceling ctx kills the child process.
type LineRunner struct{}

func (LineRunner) Run(ctx context.Context, name string, args []string, onLine func(string)) error {
	return RunLines(CommandContext(ctx, name, args...), onLine)
}

// RunLines starts cmd with stdout and stderr joined into one pipe, calls onLine
// for each line it writes, and returns once cmd has exited. A non-zero exit is
// an error.
func RunLines(cmd *exec.Cmd, onLine func(string)) error {
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return err
	}
	waitErr := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		_ = pw.CloseWithError(err) // unblocks the scanner with EOF (or the err)
		waitErr <- err
	}()
	scanErr := ScanLines(pr, onLine)
	if err := <-waitErr; err != nil {
		return err
	}
	return scanErr
}

// ScanLines calls onLine for each line r yields, until r is exhausted. A line
// ends at '\n' or '\r': spotDL and yt-dlp redraw their progress line with a
// carriage return and no newline, which a plain line scanner would buffer until
// the download finished. A line past the scanner's buffer stops the calls, but
// r is still drained, so a writer blocked on it can finish.
func ScanLines(r io.Reader, onLine func(string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sc.Split(scanLinesCR)
	for sc.Scan() {
		onLine(sc.Text())
	}
	_, _ = io.Copy(io.Discard, r)
	return sc.Err()
}

// scanLinesCR splits on either '\n' or '\r'.
func scanLinesCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
