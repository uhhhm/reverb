//go:build windows

package childproc

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestLowPriorityCommandStartsHiddenAndBelowNormal(t *testing.T) {
	cmd := LowPriorityCommand("reverb-desktop.exe", "--background")
	if cmd.SysProcAttr == nil {
		t.Fatal("LowPriorityCommand left Windows process attributes unset")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("LowPriorityCommand would show a console window")
	}
	want := uint32(windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP | windows.BELOW_NORMAL_PRIORITY_CLASS)
	if got := cmd.SysProcAttr.CreationFlags & want; got != want {
		t.Fatalf("creation flags = %#x, want hidden process group at below-normal priority (%#x)", got, want)
	}
}
