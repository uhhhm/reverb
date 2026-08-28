package p2p

import (
	"context"
	"encoding/json"
	"log"
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
func RegisterSyncHandler(h host.Host, store *sync.SyncStore, guard *Guard, keys DeviceKeyStore) {
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
		// Learn any device keys the peer knows before verifying authorship, so
		// a relayed change from a device we have never paired with can be
		// checked against its own key.
		ApplyDeviceAnnouncements(ctx, keys, req.Devices)
		accepted, refused := filterAuthorizedChanges(ctx, store, deviceID, req.Changes)
		if refused > 0 {
			log.Printf("p2p sync: dropped %d unverifiable change(s) from device %s", refused, deviceID)
		}
		req.Changes = accepted
		guard.Touch(ctx, s.Conn().RemotePeer())

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
			Devices:     LocalDeviceAnnouncements(ctx, keys),
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

// filterAuthorizedChanges keeps only the changes a peer is entitled to deliver.
//
// A change authored by the peer itself is accepted on the strength of the
// authenticated connection. A change authored by anyone else is accepted only
// if it carries a valid signature from that author, which is what makes relayed
// sync safe: the relaying peer never has to be trusted to speak for the author.
//
// If a peer-authored change carries a signature, it is verified. A corrupt
// signature would be stored as-is and become an unrelayable row that fails
// VerifyChangeAuthorship on the next hop, so it is rejected here.
func filterAuthorizedChanges(ctx context.Context, store *sync.SyncStore, peerDevice string, in []sync.SyncChange) ([]sync.SyncChange, int) {
	out := make([]sync.SyncChange, 0, len(in))
	refused := 0
	for _, ch := range in {
		if ch.DeviceID == "" || ch.DeviceID == peerDevice {
			ch.DeviceID = peerDevice
			if ch.Sig != "" {
				if err := store.VerifyChangeAuthorship(ctx, ch); err != nil {
					refused++
					continue
				}
			}
			out = append(out, ch)
			continue
		}
		if err := store.VerifyChangeAuthorship(ctx, ch); err != nil {
			refused++
			continue
		}
		out = append(out, ch)
	}
	return out, refused
}
