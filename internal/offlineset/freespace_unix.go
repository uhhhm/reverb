//go:build unix

package offlineset

import "golang.org/x/sys/unix"

// FreeSpace reports the bytes an unprivileged process may still write on the
// disk holding dir.
func FreeSpace(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
