//go:build windows

package childproc

import (
	"context"
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

func TestGracefulCommandStartsHiddenWithAControllableProcessGroup(t *testing.T) {
	cmd := GracefulCommandContext(context.Background(), "navidrome.exe")
	if cmd.SysProcAttr == nil {
		t.Fatal("GracefulCommandContext left Windows process attributes unset")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("GracefulCommandContext would show a console window")
	}
	if got := cmd.SysProcAttr.CreationFlags; got&windows.CREATE_NEW_PROCESS_GROUP != 0 {
		t.Fatalf("creation flags = %#x, CREATE_NEW_PROCESS_GROUP is ignored with CREATE_NEW_CONSOLE", got)
	}
	if got := cmd.SysProcAttr.CreationFlags; got&windows.CREATE_NEW_CONSOLE == 0 {
		t.Fatalf("creation flags = %#x, want a dedicated console when the GUI parent has none", got)
	}
	if got := cmd.SysProcAttr.CreationFlags; got&windows.CREATE_NO_WINDOW != 0 {
		t.Fatalf("creation flags = %#x, CREATE_NO_WINDOW prevents graceful Ctrl-Break shutdown", got)
	}
}
