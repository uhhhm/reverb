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
	localDeviceID string
	interval      time.Duration
	mu            stdsync.Mutex
	peerVectors   map[peer.ID]map[string]int64
}

func NewSyncer(h host.Host, store *reverbsync.SyncStore, localDeviceID string) *Syncer {
	if h == nil {
		return &Syncer{store: store, localDeviceID: localDeviceID, interval: 30 * time.Second, peerVectors: make(map[peer.ID]map[string]int64)}
	}
	return &Syncer{host: h, store: store, localDeviceID: localDeviceID, interval: 30 * time.Second, peerVectors: make(map[peer.ID]map[string]int64)}
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
	if s.host == nil || s.store == nil {
		return
	}
	for _, pid := range s.host.Network().Peers() {
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
	var changes []reverbsync.SyncChange
	if len(peerVec) > 0 {
		changes, err = s.store.ListSinceVector(ctx, peerVec, 10000)
		if err != nil {
			log.Printf("p2p syncer: ListSinceVector failed for %s: %v", pid, err)
			return err
		}
	} else {
		changes, err = s.store.ListSince(ctx, 0, 10000)
		if err != nil {
			log.Printf("p2p syncer: ListSince failed for %s: %v", pid, err)
			return err
		}
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
	if err := json.NewDecoder(st).Decode(&resp); err != nil {
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
	// Apply outbound changes from peer via Reconcile. Changes retain their
	// original DeviceID (author); we group by DeviceID to avoid attributing all
	// to the libp2p peer ID (which is not a Reverb device).
	if len(resp.Changes) > 0 {
		byDevice := make(map[string][]reverbsync.SyncChange)
		for _, ch := range resp.Changes {
			did := ch.DeviceID
			if did == "" {
				continue
			}
			byDevice[did] = append(byDevice[did], ch)
		}
		for did, batch := range byDevice {
			if _, _, _, err := s.store.Reconcile(ctx, did, 0, batch); err != nil {
				log.Printf("p2p syncer: Reconcile failed for device %s from %s: %v", did, pid, err)
			}
		}
	}
	return nil
}
