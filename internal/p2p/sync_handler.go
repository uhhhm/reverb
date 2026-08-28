package p2p

import (
	"context"
	"encoding/json"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/uhhhm/reverb/internal/sync"
)

// RegisterSyncHandler mounts /reverb/sync/1.0.0 on the host.
//
// Identity comes from the libp2p connection, not from the message body. The
// device ID a peer claims in SyncRequest is only honoured when it matches the
// device bound to that peer at pairing time; a device ID is not a secret (it
// travels in the author field of every change) and cannot authenticate anyone.
func RegisterSyncHandler(h host.Host, store *sync.SyncStore, guard *Guard) {
	h.SetStreamHandler("/reverb/sync/1.0.0", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(30 * time.Second))
		if guard == nil {
			_ = s.Reset()
			return
		}
		ctx := context.Background()
		boundDevice, rejected := guard.rejectUntrusted(ctx, s)
		if rejected {
			return
		}
		if boundDevice == "" {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": "peer has no device binding: re-pair required"})
			return
		}
		var req sync.SyncRequest
		if err := decodeLimited(s, maxSyncMessageBytes, &req); err != nil {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": err.Error()})
			return
		}
		// The connection decides who this is. A mismatched claim is a forgery
		// attempt, not a recoverable error.
		if req.DeviceID != "" && req.DeviceID != boundDevice {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": "deviceId does not match paired identity"})
			return
		}
		deviceID := boundDevice
		if err := store.ValidateDevice(ctx, deviceID); err != nil {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": "unknown deviceId: pairing required"})
			return
		}
		// Inbound changes may only be authored by the peer we authenticated.
		// Accepting a third party's device ID here would let any paired peer
		// forge changes as any other device.
		for i := range req.Changes {
			if req.Changes[i].DeviceID != "" && req.Changes[i].DeviceID != deviceID {
				_ = json.NewEncoder(s).Encode(map[string]string{"error": "change author does not match paired identity"})
				return
			}
			req.Changes[i].DeviceID = deviceID
		}
		_ = guard.store.TouchTrustedPeer(ctx, s.Conn().RemotePeer().String())

		sinceRev := req.SinceRevision
		// Vector-based filtering: if peer supplied vector, we filter outbound to only
		// changes the peer hasn't seen (seq > peerVector[deviceID]).
		peerVector := req.Vector
		_ = req.SinceHLC

		outbound, newRev, rejectedChanges, err := store.Reconcile(ctx, deviceID, sinceRev, req.Changes)
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
		newHLC, err := store.GetMaxHLC(ctx)
		if err != nil {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": err.Error()})
			return
		}
		seqMap, _, err := store.GetVectorMap(ctx)
		if err != nil {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": err.Error()})
			return
		}
		resp := sync.SyncResponse{
			Changes:     outbound,
			NewRevision: newRev,
			NewHLC:      newHLC,
			Vector:      seqMap,
			Accepted:    len(req.Changes) - len(rejectedChanges),
			Rejected:    rejectedChanges,
		}
		_ = json.NewEncoder(s).Encode(resp)
	})
}
