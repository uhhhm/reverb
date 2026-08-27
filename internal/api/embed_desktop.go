//go:build desktop

package api

import "net/http"

// embeddedSPA in desktop mode: Wails AssetServer serves SPA.
func (s *Server) embeddedSPA() http.Handler { return http.NotFoundHandler() }
