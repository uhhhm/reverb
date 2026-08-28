package app

import "log"

// logf is the composition root's log seam. Wiring messages go to the standard
// logger in both builds.
func logf(format string, args ...any) { log.Printf(format, args...) }
