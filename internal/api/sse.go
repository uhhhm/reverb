package api

import "net/http"

// sseFlush returns a flush function for an SSE response.
//
// Flushing is an optimization, not a requirement: a ResponseWriter that cannot
// flush still delivers every byte written. The Wails desktop webview's
// ResponseWriter is exactly that case — it writes straight into the webview's
// input stream but implements no Flush — so REQUIRING http.Flusher failed every
// SSE request inside the desktop window while working fine over the plain HTTP
// listener.
func sseFlush(w http.ResponseWriter) func() {
	if f, ok := w.(http.Flusher); ok {
		return f.Flush
	}
	return func() {}
}
