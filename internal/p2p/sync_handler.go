package p2p

import (
	"context"
	"encoding/json"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/uhhhm/reverb/internal/sync"
)

// RegisterSyncHandler mounts /reverb/sync/1.0.0 on the host. It decodes a
// SyncRequest, validates the sender's DeviceID (Reverb dev_*), calls
// store.Reconcile, and encodes a SyncResponse. DeviceID is required; we do
// not fall back to the libp2p peer ID (which is unrelated to sync_change.device_id FK).
func RegisterSyncHandler(h host.Host, store *sync.SyncStore) {
	h.SetStreamHandler("/reverb/sync/1.0.0", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(30 * time.Second))
		var req sync.SyncRequest
		if err := json.NewDecoder(s).Decode(&req); err != nil {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": err.Error()})
			return
		}
		deviceID := req.DeviceID
		if deviceID == "" {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": "missing deviceId: pairing required"})
			return
		}
		sinceRev := req.SinceRevision
		// Vector-based filtering: if peer supplied vector, we filter outbound to only
		// changes the peer hasn't seen (seq > peerVector[deviceID]).
		peerVector := req.Vector
		_ = req.SinceHLC

		ctx := context.Background()
		outbound, newRev, rejected, err := store.Reconcile(ctx, deviceID, sinceRev, req.Changes)
		if err != nil {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": err.Error()})
			return
		}
		// Vector-based outbound: page via ListSinceVector so filtering does not
		// truncate when the log exceeds the 10k cap (Reconcile's ListSince uses
		// sinceRev+limit and filtering afterwards would drop the tail).
		if len(peerVector) > 0 {
			vecOutbound, vecErr := store.ListSinceVector(ctx, peerVector, 10000)
			if vecErr != nil {
				_ = json.NewEncoder(s).Encode(map[string]string{"error": vecErr.Error()})
				return
			}
			outbound = vecOutbound
		}
		newHLC, _ := store.GetMaxHLC(ctx)
		seqMap, _, _ := store.GetVectorMap(ctx)
		resp := sync.SyncResponse{
			Changes:     outbound,
			NewRevision: newRev,
			NewHLC:      newHLC,
			Vector:      seqMap,
			Accepted:    len(req.Changes) - len(rejected),
			Rejected:    rejected,
		}
		_ = json.NewEncoder(s).Encode(resp)
	})
}
