package api

import (
	"context"
	"net/http"
)

// UpdateService is the desktop self-update slice the API exposes.
//
// Status returns an opaque JSON-encodable snapshot rather than a concrete type
// so this package stays independent of the desktop tree: the updater has to
// replace the running binary, which only the desktop build knows how to do.
// Nil in server builds, where the container image is the update mechanism.
type UpdateService interface {
	Status() any
	// Check re-reads the release feed and downloads a newer build. It blocks
	// for the duration of the download.
	Check(ctx context.Context)
	// Install swaps in the downloaded build and restarts the app.
	Install() error
	// Dismiss stops prompting for the currently offered version without
	// discarding the download.
	Dismiss()
}

// updateReady reports the service, or writes 503 when this build has no
// updater wired.
func (s *Server) updateReady(w http.ResponseWriter) (UpdateService, bool) {
	if s.deps.Update == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "updates unavailable"})
		return nil, false
	}
	return s.deps.Update, true
}

// handleUpdateStatus reports what the app knows about updates: the running
// version, any newer release, download progress, and whether a build is staged
// and waiting for a restart.
func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.updateReady(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, svc.Status())
}

// handleUpdateCheck forces a release check. The check and any download run in
// the background — the response says only that the check started, and progress
// arrives over the event stream.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.updateReady(w)
	if !ok {
		return
	}
	// Deliberately detached from the request context: the download must not be
	// cancelled by the browser navigating away.
	go svc.Check(context.Background())
	writeJSON(w, http.StatusAccepted, svc.Status())
}

// handleUpdateInstall applies the staged build and restarts. It is the only
// path that touches the binary, and it exists only because the user asked.
func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.updateReady(w)
	if !ok {
		return
	}
	if err := svc.Install(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

// handleUpdateDismiss silences the prompt for the offered version. The download
// is kept, so choosing "Later" costs nothing but the prompt.
func (s *Server) handleUpdateDismiss(w http.ResponseWriter, r *http.Request) {
	svc, ok := s.updateReady(w)
	if !ok {
		return
	}
	svc.Dismiss()
	writeJSON(w, http.StatusOK, svc.Status())
}
