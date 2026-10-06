package ytdlp

import "context"

// Runner streams a process's combined stdout/stderr line-by-line. Abstracted so
// the parser is unit-testable with canned output and no real downloads occur.
type Runner interface {
	Run(ctx context.Context, name string, args []string, onLine func(string)) error
}
