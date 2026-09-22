//go:build windows

package offlineset

import "golang.org/x/sys/windows"

// FreeSpace reports the bytes the caller may still write on the disk holding
// dir.
func FreeSpace(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, nil, nil); err != nil {
		return 0, err
	}
	return int64(free), nil
}
