package api

import (
	"context"
	"net"
	"net/http"

	"github.com/uhhhm/reverb/internal/p2p"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// p2pHost returns the current p2p host via Deps.P2P provider, or nil.
func (s *Server) p2pHost() *p2p.Host {
	if s.deps.P2P == nil {
		return nil
	}
	return s.deps.P2P()
}

func (s *Server) handleP2PStatus(w http.ResponseWriter, r *http.Request) {
	h := s.p2pHost()
	if h == nil || h.LibHost() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "p2p unavailable"})
		return
	}
	// Gather vector and max HLC from sync store.
	var vector map[string]int64
	var hlc int64
	if s.deps.SyncStore != nil {
		if seqMap, _, err := s.deps.SyncStore.GetVectorMap(r.Context()); err == nil {
			vector = seqMap
		}
		if v, err := s.deps.SyncStore.GetMaxHLC(r.Context()); err == nil {
			hlc = v
		}
	}
	peers := h.LibHost().Network().Peers()
	peerCount := len(peers)
	writeJSON(w, http.StatusOK, map[string]any{
		"peerId": h.ID(),
		"addrs":  h.Addrs(),
		// dialAddrs are the complete /p2p/-terminated addresses another device
		// can be given to reach this one. They are what the pairing UI shows,
		// because on a VPN the peer ID alone is not dialable: mDNS multicast
		// does not cross the tunnel and the DHT knows nothing of these hosts.
		"dialAddrs": h.DialAddrs(),
		"peerCount": peerCount,
		"vector":    vector,
		"hlc":       hlc,
	})
}

func (s *Server) handleP2PPeers(w http.ResponseWriter, r *http.Request) {
	h := s.p2pHost()
	if h == nil || h.LibHost() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "p2p unavailable"})
		return
	}
	peers := h.LibHost().Network().Peers()
	out := make([]map[string]any, 0, len(peers))
	for _, pid := range peers {
		ci := h.LibHost().Network().ConnsToPeer(pid)
		addrs := h.LibHost().Peerstore().Addrs(pid)
		strAddrs := make([]string, 0, len(addrs))
		for _, a := range addrs {
			strAddrs = append(strAddrs, a.String())
		}
		out = append(out, map[string]any{
			"peerId":    pid.String(),
			"addrs":     strAddrs,
			"conns":     len(ci),
			"connected": len(ci) > 0,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type p2pRedeemRequest struct {
	// PeerID is either a bare peer ID or a full multiaddr ending in
	// /p2p/<peerID>. The bare form works only where discovery has already found
	// the peer (a LAN, via mDNS); over a VPN the full multiaddr is required.
	PeerID     string `json:"peerId"`
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

func (s *Server) handleP2PRedeem(w http.ResponseWriter, r *http.Request) {
	h := s.p2pHost()
	if h == nil || h.LibHost() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "p2p unavailable"})
		return
	}
	var body p2pRedeemRequest
	if err := decode(r, &body); err != nil || body.PeerID == "" || body.Code == "" || body.DeviceName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "peerId, code and deviceName are required"})
		return
	}
	var guard *p2p.Guard
	if s.deps.P2PGuard != nil {
		guard = s.deps.P2PGuard()
	}
	if guard == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "peer trust store unavailable"})
		return
	}
	deviceID, token, err := p2p.RedeemViaPeer(r.Context(), h.LibHost(), guard, s.deps.DeviceKeys, body.PeerID, body.Code, body.DeviceName, s.localSyncDeviceID(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deviceId": deviceID, "token": token})
}

func (s *Server) handleP2PManifests(w http.ResponseWriter, r *http.Request) {
	if s.deps.FileStore == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	rows, err := s.deps.FileStore.ListFileManifests(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type p2pFetchRequest struct {
	PeerID      string `json:"peerId"`
	RelPath     string `json:"relPath"`
	ContentHash string `json:"contentHash"`
}

func (s *Server) handleP2PFetch(w http.ResponseWriter, r *http.Request) {
	h := s.p2pHost()
	if h == nil || h.LibHost() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "p2p unavailable"})
		return
	}
	var body p2pFetchRequest
	if err := decode(r, &body); err != nil || body.PeerID == "" || body.RelPath == "" || body.ContentHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "peerId, relPath and contentHash are required"})
		return
	}
	musicDir := s.deps.MusicDir
	if musicDir == "" && s.deps.DataDir != "" {
		musicDir = s.deps.DataDir + "/music"
	}
	var deviceID string
	if s.deps.PairingStore != nil {
		if id, err := reverbsync.LocalDeviceID(r.Context(), s.deps.PairingStore); err == nil && id != "" {
			deviceID = id
		}
	}
	if deviceID == "" && s.deps.SyncStore != nil {
		if id, err := s.deps.SyncStore.LocalDeviceID(r.Context()); err == nil && id != "" {
			deviceID = id
		} else if seqMap, _, err := s.deps.SyncStore.GetVectorMap(r.Context()); err == nil && len(seqMap) > 0 {
			// Legacy fallback: deterministic smallest lex deviceId (covers fresh DB where settings not yet migrated).
			for k := range seqMap {
				if deviceID == "" || k < deviceID {
					deviceID = k
				}
			}
		}
	}
	if deviceID == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "local device not initialized: pairing required"})
		return
	}
	var store p2p.FileStore
	if s.deps.FileStore != nil {
		store = s.deps.FileStore
	} else if fs, ok := any(s.deps.PairingStore).(p2p.FileStore); ok {
		store = fs
	}
	if store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "file store unavailable"})
		return
	}
	if musicDir != "" {
		fs := p2p.NewFileSyncer(store, deviceID, musicDir)
		if err := fs.FetchFileViaPeer(r.Context(), h.LibHost(), body.PeerID, body.RelPath, body.ContentHash); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// invalidateP2PTrust drops the cached peer trust set. Called after a device is
// deleted so its peer loses access without waiting for the cache TTL.
func (s *Server) invalidateP2PTrust() {
	if s.deps.P2PGuard == nil {
		return
	}
	if g := s.deps.P2PGuard(); g != nil {
		g.Invalidate()
	}
}

// pairingClientKey identifies the caller of the unauthenticated pairing redeem
// endpoint for rate-limiting purposes. It uses the transport peer address only:
// X-Forwarded-For is attacker-controlled and would let one client masquerade as
// an unlimited number of distinct keys.
func pairingClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// localSyncDeviceID is the device ID this node authors sync changes under. It
// is read from the syncer where possible, since that is the value that travels
// on every sync round and therefore the one a peer must bind us to; the store
// lookup is the same resolution the syncer itself was built from, for the case
// where p2p is up but the syncer is not.
func (s *Server) localSyncDeviceID(ctx context.Context) string {
	if s.deps.P2PSyncer != nil {
		if syncer := s.deps.P2PSyncer(); syncer != nil {
			if id := syncer.LocalDeviceID(); id != "" {
				return id
			}
		}
	}
	if s.deps.PairingStore != nil {
		if id, err := reverbsync.LocalDeviceID(ctx, s.deps.PairingStore); err == nil {
			return id
		}
	}
	return ""
}
