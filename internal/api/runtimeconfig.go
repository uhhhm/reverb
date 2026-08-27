package api

import (
	"fmt"
	"net/http"
)

// handleRuntimeConfig serves the bootstrap script that hands the SPA its real
// HTTP origin as window.__REVERB_PORT__.
//
// The desktop app needs this because Wails serves the SPA through its
// AssetServer, whose ResponseWriter cannot hijack a connection — so a WebSocket
// can never upgrade over that transport. The page has to dial the plain
// 127.0.0.1 listener directly, and only the server knows which port it landed
// on (the desktop binds :0). Served as a file rather than injected inline
// because the CSP is script-src 'self' with no unsafe-inline.
//
// It is unauthenticated: it exposes a local port number the page can already
// reach, and it must load before the session exists.
func (s *Server) handleRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if s.deps.LocalAPIPort == 0 {
		// Same-origin deployments derive everything from location; nothing to inject.
		_, _ = w.Write([]byte("// no runtime config\n"))
		return
	}
	_, _ = fmt.Fprintf(w, "window.__REVERB_PORT__ = %d;\n", s.deps.LocalAPIPort)
}
