package api

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// spaHandler serves the frontend. In dev it proxies to Vite; in prod it serves
// the embedded build. In desktop mode the Wails AssetServer serves the SPA, so
// the HTTP handler returns NotFound.
func (s *Server) spaHandler() http.Handler {
	if s.deps.Desktop {
		return s.embeddedSPA()
	}
	if s.deps.Dev {
		target, _ := url.Parse("http://localhost:5173")
		return httputil.NewSingleHostReverseProxy(target)
	}
	return s.embeddedSPA()
}
