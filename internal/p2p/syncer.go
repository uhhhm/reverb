package p2p

import (
	"context"
	"encoding/json"
	"log"
	stdsync "sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// Syncer drives anti-entropy between peers via /reverb/sync/1.0.0.
// Every 30s it iterates connected peers and exchanges vectors.
type Syncer struct {
	host          host.Host
	store         *reverbsync.SyncStore
	guard         *Guard
	localDeviceID string
	interval      time.Duration
	mu            stdsync.Mutex
	peerVectors   map[peer.ID]map[string]int64
}

func NewSyncer(h host.Host, store *reverbsync.SyncStore, guard *Guard, localDeviceID string) *Syncer {
	if h == nil {
		return &Syncer{store: store, guard: guard, localDeviceID: localDeviceID, interval: 30 * time.Second, peerVectors: make(map[peer.ID]map[string]int64)}
	}
	return &Syncer{host: h, store: store, guard: guard, localDeviceID: localDeviceID, interval: 30 * time.Second, peerVectors: make(map[peer.ID]map[string]int64)}
}

// Run blocks until ctx canceled, ticking every interval.
func (s *Syncer) Run(ctx context.Context) error {
	if s.host == nil || s.store == nil {
		<-ctx.Done()
		return nil
	}
	t := time.NewTicker(s.interval)
	defer t.Stop()
	// Initial sync shortly after start.
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(5 * time.Second):
		s.syncAll(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.syncAll(ctx)
		}
	}
}

func (s *Syncer) syncAll(ctx context.Context) {
	if s.host == nil || s.store == nil || s.guard == nil {
		return
	}
	// Only paired peers. mDNS auto-connects to anything advertising the service
	// tag, so "connected" carries no trust on its own; pushing the change log to
	// every connection would hand our device ID and library metadata to any
	// listener on the network.
	trusted, err := s.guard.TrustedPeers(ctx)
	if err != nil {
		log.Printf("p2p syncer: trusted peer lookup failed: %v", err)
		return
	}
	if len(trusted) == 0 {
		return
	}
	for _, pid := range s.host.Network().Peers() {
		if _, ok := trusted[pid]; !ok {
			continue
		}
		_ = s.syncPeer(ctx, pid)
	}
}

func (s *Syncer) syncPeer(ctx context.Context, pid peer.ID) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, err := s.host.NewStream(ctx, pid, "/reverb/sync/1.0.0")
	if err != nil {
		return err
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(15 * time.Second))

	// Send our vector and recent changes. Peer filters its outbound by our vector.
	// If we have a remembered peer vector, only send changes the peer hasn't seen.
	seqMap, _, err := s.store.GetVectorMap(ctx)
	if err != nil {
		log.Printf("p2p syncer: GetVectorMap failed for %s: %v", pid, err)
		seqMap = nil
	}
	s.mu.Lock()
	peerVec := s.peerVectors[pid]
	s.mu.Unlock()
	changes, err := s.store.ListSinceVector(ctx, peerVec, 10000)
	if err != nil {
		log.Printf("p2p syncer: ListSinceVector failed for %s: %v", pid, err)
		return err
	}
	req := reverbsync.SyncRequest{
		DeviceID: s.localDeviceID,
		Vector:   seqMap,
		Changes:  changes,
	}
	if err := json.NewEncoder(st).Encode(req); err != nil {
		return err
	}
	_ = st.CloseWrite()
	var resp reverbsync.SyncResponse
	if err := decodeLimited(st, maxSyncMessageBytes, &resp); err != nil {
		return err
	}
	// Remember peer's vector for next anti-entropy round.
	if len(resp.Vector) > 0 {
		s.mu.Lock()
		cp := make(map[string]int64, len(resp.Vector))
		for k, v := range resp.Vector {
			cp[k] = v
		}
		s.peerVectors[pid] = cp
		s.mu.Unlock()
	}
	// Apply changes from the peer, but only those it is entitled to author.
	// Changes are unsigned, so a relayed third-party change is indistinguishable
	// from one this peer invented; accepting them would let any paired device
	// forge history as any other. Convergence still holds for a fully paired
	// mesh, where every device exchanges directly.
	if len(resp.Changes) > 0 {
		peerDevice, derr := s.guard.DeviceFor(ctx, pid)
		if derr != nil || peerDevice == "" {
			log.Printf("p2p syncer: no device binding for peer %s, dropping %d changes", pid, len(resp.Changes))
			return nil
		}
		batch := make([]reverbsync.SyncChange, 0, len(resp.Changes))
		dropped := 0
		for _, ch := range resp.Changes {
			if ch.DeviceID != "" && ch.DeviceID != peerDevice {
				dropped++
				continue
			}
			ch.DeviceID = peerDevice
			batch = append(batch, ch)
		}
		if dropped > 0 {
			log.Printf("p2p syncer: dropped %d change(s) from %s not authored by its paired device %s", dropped, pid, peerDevice)
		}
		if len(batch) > 0 {
			if _, _, _, err := s.store.Reconcile(ctx, peerDevice, 0, batch); err != nil {
				log.Printf("p2p syncer: Reconcile failed for device %s from %s: %v", peerDevice, pid, err)
			}
		}
	}
	return nil
}
