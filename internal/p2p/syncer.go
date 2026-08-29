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
	keys          DeviceKeyStore
	localDeviceID string
	interval      time.Duration
	mu            stdsync.Mutex
	peerVectors   map[peer.ID]map[string]int64
}

func NewSyncer(h host.Host, store *reverbsync.SyncStore, guard *Guard, keys DeviceKeyStore, localDeviceID string) *Syncer {
	if h == nil {
		return &Syncer{store: store, guard: guard, keys: keys, localDeviceID: localDeviceID, interval: 30 * time.Second, peerVectors: make(map[peer.ID]map[string]int64)}
	}
	return &Syncer{host: h, store: store, guard: guard, keys: keys, localDeviceID: localDeviceID, interval: 30 * time.Second, peerVectors: make(map[peer.ID]map[string]int64)}
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
	// Iterate the trust set and dial, rather than iterating live connections.
	// Only connected peers were considered before, which silently confined sync
	// to whatever mDNS had connected -- over a VPN, where multicast does not
	// reach, that was nothing at all.
	//
	// Dial + sync per peer is done concurrently so one offline peer (15s
	// dialTimeout) does not stall the others and drift the 30s ticker.
	var wg stdsync.WaitGroup
	for pid := range trusted {
		pid := pid
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !EnsureConnected(ctx, s.host, s.guard, pid) {
				return
			}
			_ = s.syncPeer(ctx, pid)
		}()
	}
	wg.Wait()
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
		Devices:  LocalDeviceAnnouncements(ctx, s.keys),
	}
	if err := json.NewEncoder(st).Encode(req); err != nil {
		return err
	}
	_ = st.CloseWrite()
	var resp reverbsync.SyncResponse
	if err := decodeLimited(st, maxSyncMessageBytes, &resp); err != nil {
		return err
	}
	// The exchange succeeded, so this address is known-good; persist it for the
	// next restart, when discovery may have nothing to offer.
	if err := s.guard.RememberAddrs(ctx, pid, ObservedAddrs(s.host, pid)); err != nil {
		log.Printf("p2p syncer: remember addrs for %s: %v", pid, err)
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
	// Apply changes from the peer. A change it authored is accepted on the
	// strength of the authenticated connection; a change authored by anyone
	// else must carry that author's signature, so relaying a third party's
	// changes is safe without trusting the relay.
	if len(resp.Changes) > 0 {
		peerDevice, derr := s.guard.DeviceFor(ctx, pid)
		if derr != nil || peerDevice == "" {
			log.Printf("p2p syncer: no device binding for peer %s, dropping %d changes", pid, len(resp.Changes))
			return nil
		}
		ApplyDeviceAnnouncements(ctx, s.keys, resp.Devices)
		accepted, refused, why := filterAuthorizedChanges(ctx, s.store, peerDevice, resp.Changes)
		if refused > 0 {
			log.Printf("p2p syncer: dropped %d unverifiable change(s) from %s (%s)", refused, pid, why)
		}
		byDevice := make(map[string][]reverbsync.SyncChange)
		for _, ch := range accepted {
			byDevice[ch.DeviceID] = append(byDevice[ch.DeviceID], ch)
		}
		for did, batch := range byDevice {
			if _, _, _, err := s.store.Reconcile(ctx, did, 0, batch); err != nil {
				log.Printf("p2p syncer: Reconcile failed for device %s from %s: %v", did, pid, err)
			}
		}
	}
	return nil
}
