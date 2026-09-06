package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	stdsync "sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/events"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// EventBus topics published around a sync round, so the UI can show progress.
const (
	TopicSyncStarted  = "sync.started"
	TopicSyncFinished = "sync.finished"
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

	bus *events.Bus
	// runMu single-flights sync rounds: the 30s ticker and an on-demand
	// SyncNow must not overlap and double-push the change log.
	runMu    stdsync.Mutex
	statusMu stdsync.Mutex
	round    SyncRound
}

// SetBus installs the event bus used to publish sync.started / sync.finished.
// Optional — a Syncer without a bus syncs silently.
func (s *Syncer) SetBus(b *events.Bus) { s.bus = b }

// SyncRound is a snapshot of a metadata exchange. File transfers and local
// projection continue independently; completion does not claim those are done.
type SyncRound struct {
	ID         int64    `json:"id"`
	State      string   `json:"state"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt"`
	DurationMs int64    `json:"durationMs"`
	Peers      int      `json:"peers"`
	Succeeded  int      `json:"succeeded"`
	Errors     []string `json:"errors"`
}

func (s *Syncer) Status() SyncRound {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	result := s.round
	result.Errors = append([]string{}, result.Errors...)
	if result.State == "" {
		result.State = "idle"
	}
	return result
}

// RequestSync coalesces repeated clicks with an active round. The pending state
// is visible before returning, so polling works even if WebSocket frames are lost.
func (s *Syncer) RequestSync(ctx context.Context) SyncRound {
	if !s.runMu.TryLock() {
		return s.Status()
	}
	s.beginRound()
	pending := s.Status()
	go func() { defer s.runMu.Unlock(); s.runRound(ctx) }()
	return pending
}

func (s *Syncer) beginRound() {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.round = SyncRound{ID: s.round.ID + 1, State: "pending", StartedAt: time.Now().UnixMilli(), Errors: []string{}}
}

// SyncNow blocks for one round; background ticks skip an already active round.
func (s *Syncer) SyncNow(ctx context.Context) {
	if !s.runMu.TryLock() {
		return
	}
	defer s.runMu.Unlock()
	s.beginRound()
	s.runRound(ctx)
}

func (s *Syncer) runRound(ctx context.Context) {
	s.statusMu.Lock()
	s.round.State = "running"
	s.statusMu.Unlock()
	s.publish(TopicSyncStarted, nil)
	start := time.Now()
	defer func() {
		s.statusMu.Lock()
		if failure := recover(); failure != nil {
			log.Printf("p2p syncer: round panic: %v", failure)
			s.round.Errors = append(s.round.Errors, "Sync interrupted by an internal error; try again.")
		}
		s.round.State = "completed"
		if len(s.round.Errors) > 0 {
			s.round.State = "failed"
		} else if s.round.Peers == 0 {
			s.round.State = "no_peers"
		}
		s.round.FinishedAt = time.Now().UnixMilli()
		s.round.DurationMs = time.Since(start).Milliseconds()
		sort.Strings(s.round.Errors)
		s.statusMu.Unlock()
		s.publish(TopicSyncFinished, s.Status())
	}()
	s.syncAll(ctx)
}

func (s *Syncer) roundError(message string) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.round.Errors = append(s.round.Errors, message)
}

func (s *Syncer) publish(topic string, payload any) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(events.Event{Topic: topic, Payload: payload})
}

// LocalDeviceID is the identity this node authors and pushes changes under.
// Pairing offers it to the responder so the peer binding names the device that
// actually syncs.
func (s *Syncer) LocalDeviceID() string {
	if s == nil {
		return ""
	}
	return s.localDeviceID
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
		s.SyncNow(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			s.SyncNow(ctx)
		}
	}
}

func (s *Syncer) syncAll(ctx context.Context) {
	if s.host == nil || s.store == nil || s.guard == nil {
		s.roundError("Sync unavailable: device networking has not started.")
		return
	}
	// Only paired peers. mDNS auto-connects to anything advertising the service
	// tag, so "connected" carries no trust on its own; pushing the change log to
	// every connection would hand our device ID and library metadata to any
	// listener on the network.
	trusted, err := s.guard.TrustedPeers(ctx)
	if err != nil {
		log.Printf("p2p syncer: trusted peer lookup failed: %v", err)
		s.roundError("Could not read paired devices: " + err.Error())
		return
	}
	s.statusMu.Lock()
	s.round.Peers = len(trusted)
	s.statusMu.Unlock()
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
			// Contained per peer: a round reconciles and materializes data
			// the peer sent, so a panic here is reachable from peer input
			// and must not take the process down with it.
			reported := false
			defer func() {
				if !reported {
					s.roundError(fmt.Sprintf("Device %s did not complete sync.", pid))
				}
			}()
			SafeRun("sync peer", func() {
				if !EnsureConnected(ctx, s.host, s.guard, pid) {
					s.roundError(fmt.Sprintf("Device %s is unreachable. Check it is open and on the same network or VPN; verify its saved pairing address.", pid))
					reported = true // the specific failure has already been recorded
					return
				}
				if err := s.syncPeer(ctx, pid); err != nil {
					log.Printf("p2p syncer: exchange with %s failed: %v", pid, err)
					s.roundError(fmt.Sprintf("Device %s: %v", pid, err))
				} else {
					s.statusMu.Lock()
					s.round.Succeeded++
					s.statusMu.Unlock()
				}
				reported = true
			})
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
		return err
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
	// A refusal (unknown device, id mismatch, store failure) is not a quiet
	// empty round: the peer will keep refusing every 30 seconds until someone
	// re-pairs, so say what happened rather than logging nothing.
	if resp.Error != "" {
		log.Printf("p2p syncer: peer %s refused sync: %s", pid, resp.Error)
		return fmt.Errorf("peer %s refused sync: %s", pid, resp.Error)
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
			return fmt.Errorf("paired device binding unavailable")
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
		// Each author's changes reconcile separately, so a play authored by one
		// device can be projected before the catalog entity another device
		// minted for the same track -- and the play then fails the
		// plays.catalog_id foreign key with the log already committed, so
		// nothing ever retries it. Catalog entities from every author go first.
		for _, catalogOnly := range []bool{true, false} {
			for did, batch := range byDevice {
				part := filterCatalog(batch, catalogOnly)
				if len(part) == 0 {
					continue
				}
				// The syncer pulls in its own step, so the outbound half of a
				// reconcile is read and thrown away; ask for none.
				if _, _, _, err := s.store.ReconcileBatchedAsync(ctx, did, reverbsync.NoOutbound, part); err != nil {
					return fmt.Errorf("apply changes: %w", err)
				}
			}
		}
		if refused > 0 {
			return fmt.Errorf("%d unverifiable changes refused: %s", refused, why)
		}
	}
	return nil
}

// filterCatalog splits a batch into its catalog entities and everything else,
// preserving order within each half.
func filterCatalog(batch []reverbsync.SyncChange, catalog bool) []reverbsync.SyncChange {
	out := make([]reverbsync.SyncChange, 0, len(batch))
	for _, ch := range batch {
		if (ch.EntityType == reverbsync.EntityCatalog) == catalog {
			out = append(out, ch)
		}
	}
	return out
}
