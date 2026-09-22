//go:build !unix && !windows

package offlineset

import "math"

// FreeSpace has no way to ask on this platform, so it never limits fetching.
func FreeSpace(string) (int64, error) { return math.MaxInt64, nil }
