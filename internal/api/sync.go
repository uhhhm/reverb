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
	GetMaxHLC(ctx context.Context) (int64, error)
	GetVectorMap(ctx context.Context) (map[string]int64, map[string]int64, error)
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
	// Gate authorship the same way the P2P handler does. Reconcile stores each
	// change under the deviceId the body names, so without this a caller could
	// author changes as any other device -- forging tombstones and field edits
	// that win conflict resolution and replicate to every peer.
	submitted := len(req.Changes)
	var unauthorized []sync.SyncChange
	req.Changes, unauthorized = s.deps.SyncStore.AuthorizeInbound(r.Context(), deviceID, req.Changes)
	outbound, newRev, rejected, err := s.deps.SyncStore.ReconcileBatched(r.Context(), deviceID, req.SinceRevision, req.Changes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rejected = append(rejected, unauthorized...)
	if outbound == nil {
		outbound = []sync.SyncChange{}
	}
	if rejected == nil {
		rejected = []sync.SyncChange{}
	}
	accepted := submitted - len(rejected)
	if accepted < 0 {
		accepted = 0
	}
	// P2P vector/hlc for new clients (best-effort).
	var newHLC int64
	var vector map[string]int64
	if h, err := s.deps.SyncStore.GetMaxHLC(r.Context()); err == nil {
		newHLC = h
	}
	if seqMap, _, err := s.deps.SyncStore.GetVectorMap(r.Context()); err == nil {
		vector = seqMap
	}
	resp := sync.SyncResponse{
		Changes:     outbound,
		NewRevision: newRev,
		NewHLC:      newHLC,
		Vector:      vector,
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
	newHLC, _ := s.deps.SyncStore.GetMaxHLC(r.Context())
	seqMap, hlcMap, _ := s.deps.SyncStore.GetVectorMap(r.Context())
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
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev, "hlc": newHLC, "vector": seqMap, "hlcVector": hlcMap, "deviceCount": count})
}

// handleSyncTrigger kicks off one on-demand anti-entropy round with paired
// peers. It returns immediately — progress is reported over the WebSocket as
// sync.started / sync.finished, since a round can take as long as the dial
// timeout of the slowest peer.
func (s *Server) handleSyncTrigger(w http.ResponseWriter, r *http.Request) {
	// Authenticate like the other sync routes: a Bearer token that names a paired
	// device, or a request that actually arrived over loopback. Without this the
	// route was reachable by anything that got past the guards on header shape
	// alone, with no token ever checked.
	if _, err := s.authenticateSync(r); err != nil {
		if errors.Is(err, sync.ErrInvalidToken) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve device"})
		return
	}
	if s.deps.P2PSyncer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sync unavailable"})
		return
	}
	syncer := s.deps.P2PSyncer()
	if syncer == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sync unavailable"})
		return
	}
	go syncer.SyncNow(context.WithoutCancel(r.Context()))
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}
