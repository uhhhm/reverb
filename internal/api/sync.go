package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/uhhhm/reverb/internal/store/db"
	"github.com/uhhhm/reverb/internal/sync"
)

// SyncStoreInterface is the seam sync handlers need.
// *sync.SyncStore satisfies it.
type SyncStoreInterface interface {
	Reconcile(ctx context.Context, deviceID string, sinceRev int64, inbound []sync.SyncChange) (outbound []sync.SyncChange, newRev int64, rejected []sync.SyncChange, err error)
	GetMaxRevision(ctx context.Context) (int64, error)
}

// ensure SyncStore implements it.
var _ SyncStoreInterface = (*sync.SyncStore)(nil)

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if s.deps.SyncStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sync unavailable"})
		return
	}
	deviceID, err := s.authenticateSync(r)
	if err != nil {
		if errors.Is(err, sync.ErrInvalidToken) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		// No server device yet -> treat as unavailable
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve device"})
		return
	}
	var req sync.SyncRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid sync request"})
		return
	}
	if req.Changes == nil {
		req.Changes = []sync.SyncChange{}
	}
	outbound, newRev, rejected, err := s.deps.SyncStore.Reconcile(r.Context(), deviceID, req.SinceRevision, req.Changes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if outbound == nil {
		outbound = []sync.SyncChange{}
	}
	if rejected == nil {
		rejected = []sync.SyncChange{}
	}
	accepted := len(req.Changes) - len(rejected)
	if accepted < 0 {
		accepted = 0
	}
	resp := sync.SyncResponse{
		Changes:     outbound,
		NewRevision: newRev,
		Accepted:    accepted,
		Rejected:    rejected,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.SyncStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sync unavailable"})
		return
	}
	if _, err := s.authenticateSync(r); err != nil {
		if errors.Is(err, sync.ErrInvalidToken) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve device"})
		return
	}
	rev, err := s.deps.SyncStore.GetMaxRevision(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	var count int
	if s.deps.PairingStore != nil {
		if devices, err := s.deps.PairingStore.ListDevices(r.Context()); err == nil {
			count = len(devices)
		}
	} else if s.deps.OfflineSet != nil {
		if devices, err := s.deps.OfflineSet.ListDevices(r.Context()); err == nil {
			count = len(devices)
		}
	} else {
		// fallback: try via db if SyncStore underlying has ListDevices via type assertion
		if lister, ok := any(s.deps.SyncStore).(interface {
			ListDevices(context.Context) ([]db.Device, error)
		}); ok {
			if devices, err := lister.ListDevices(r.Context()); err == nil {
				count = len(devices)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev, "deviceCount": count})
}
