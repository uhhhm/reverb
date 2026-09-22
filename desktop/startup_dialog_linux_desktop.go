//go:build linux && desktop

package main

/*
#cgo pkg-config: gtk+-3.0
#include <stdlib.h>
#include <gtk/gtk.h>

static int reverb_show_startup_error(const char *title, const char *message) {
	if (!gtk_init_check(NULL, NULL)) {
		return 0;
	}
	GtkWidget *dialog = gtk_message_dialog_new(
		NULL,
		GTK_DIALOG_MODAL,
		GTK_MESSAGE_ERROR,
		GTK_BUTTONS_CLOSE,
		"%s",
		message
	);
	gtk_window_set_title(GTK_WINDOW(dialog), title);
	gtk_dialog_run(GTK_DIALOG(dialog));
	gtk_widget_destroy(dialog);
	while (gtk_events_pending()) {
		gtk_main_iteration();
	}
	return 1;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// showStartupErrorDialog implements startupErrorDialog with the same GTK
// runtime the Linux Wails window uses, so no optional dialog utility is needed.
func showStartupErrorDialog(title, message string) error {
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cMessage := C.CString(message)
	defer C.free(unsafe.Pointer(cMessage))
	if C.reverb_show_startup_error(cTitle, cMessage) == 0 {
		return fmt.Errorf("GTK could not connect to a display")
	}
	return nil
}
