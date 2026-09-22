//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// showStartupErrorDialog implements startupErrorDialog for Windows.
func showStartupErrorDialog(title, message string) error {
	title16, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	message16, err := windows.UTF16PtrFromString(message)
	if err != nil {
		return err
	}
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	const mbOKIconError = 0x00000010
	result, _, callErr := proc.Call(0, uintptr(unsafe.Pointer(message16)), uintptr(unsafe.Pointer(title16)), mbOKIconError)
	if result == 0 {
		return fmt.Errorf("MessageBoxW: %w", callErr)
	}
	return nil
}
