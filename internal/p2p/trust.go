package p2p

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/store/db"
)

// ErrUntrustedPeer is returned when a libp2p peer has not completed pairing.
var ErrUntrustedPeer = errors.New("untrusted peer: pairing required")

// TrustStore is the minimal store seam for the p2p peer trust set.
// *db.Queries satisfies it.
type TrustStore interface {
	TrustPeer(ctx context.Context, arg db.TrustPeerParams) error
	GetTrustedPeer(ctx context.Context, peerID string) (db.P2pPeer, error)
	ListTrustedPeers(ctx context.Context) ([]db.P2pPeer, error)
	TouchTrustedPeer(ctx context.Context, peerID string) error
}

// trustTTL bounds how long a positive trust lookup is cached. Short enough that
// unpairing a device takes effect promptly, long enough that the 30s
// anti-entropy tick does not hit SQLite for every peer on every round.
const trustTTL = 30 * time.Second

type trustEntry struct {
	deviceID string
	at       time.Time
}

// Guard answers "is this libp2p peer paired, and which device is it?" for the
// stream handlers and the outbound syncer. Negative results are never cached:
// a peer that pairs must become usable immediately.
type Guard struct {
	store TrustStore
	mu    sync.RWMutex
	cache map[peer.ID]trustEntry
}

func NewGuard(store TrustStore) *Guard {
	return &Guard{store: store, cache: make(map[peer.ID]trustEntry)}
}

// DeviceFor returns the device ID bound to pid, or ErrUntrustedPeer if the peer
// has not paired. A trusted peer with no device binding yields an empty string
// with a nil error — it is trusted but cannot assert a device identity.
func (g *Guard) DeviceFor(ctx context.Context, pid peer.ID) (string, error) {
	if g == nil || g.store == nil {
		return "", ErrUntrustedPeer
	}
	g.mu.RLock()
	e, ok := g.cache[pid]
	g.mu.RUnlock()
	if ok && time.Since(e.at) < trustTTL {
		return e.deviceID, nil
	}
	row, err := g.store.GetTrustedPeer(ctx, pid.String())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrUntrustedPeer
		}
		return "", err
	}
	deviceID := ""
	if row.DeviceID.Valid {
		deviceID = row.DeviceID.String
	}
	g.mu.Lock()
	g.cache[pid] = trustEntry{deviceID: deviceID, at: time.Now()}
	g.mu.Unlock()
	return deviceID, nil
}

// Allowed reports whether pid has completed pairing.
func (g *Guard) Allowed(ctx context.Context, pid peer.ID) bool {
	_, err := g.DeviceFor(ctx, pid)
	return err == nil
}

// Trust adds pid to the trust set, binding it to deviceID when non-empty.
// Called from both sides of a successful pairing exchange.
func (g *Guard) Trust(ctx context.Context, pid peer.ID, deviceID, name string) error {
	if g == nil || g.store == nil {
		return errors.New("no trust store")
	}
	arg := db.TrustPeerParams{PeerID: pid.String(), Name: name}
	if deviceID != "" {
		arg.DeviceID = sql.NullString{String: deviceID, Valid: true}
	}
	if err := g.store.TrustPeer(ctx, arg); err != nil {
		return err
	}
	g.mu.Lock()
	g.cache[pid] = trustEntry{deviceID: deviceID, at: time.Now()}
	g.mu.Unlock()
	return nil
}

// Touch records that a paired peer was seen, for the devices UI.
func (g *Guard) Touch(ctx context.Context, pid peer.ID) {
	if g == nil || g.store == nil {
		return
	}
	_ = g.store.TouchTrustedPeer(ctx, pid.String())
}

// Forget drops pid from the local cache so the next lookup re-reads the DB.
// Call after unpairing a device.
func (g *Guard) Forget(pid peer.ID) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.cache, pid)
	g.mu.Unlock()
}

// Invalidate clears the whole cache. Call after any device deletion, since a
// deleted device cascades to its p2p_peer rows.
func (g *Guard) Invalidate() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.cache = make(map[peer.ID]trustEntry)
	g.mu.Unlock()
}

// TrustedPeers returns the current trust set as a lookup map, for callers that
// need to filter a peer list in one pass (the syncer).
func (g *Guard) TrustedPeers(ctx context.Context) (map[peer.ID]string, error) {
	if g == nil || g.store == nil {
		return nil, errors.New("no trust store")
	}
	rows, err := g.store.ListTrustedPeers(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[peer.ID]string, len(rows))
	for _, r := range rows {
		pid, err := peer.Decode(r.PeerID)
		if err != nil {
			continue
		}
		deviceID := ""
		if r.DeviceID.Valid {
			deviceID = r.DeviceID.String
		}
		out[pid] = deviceID
	}
	return out, nil
}

// rejectUntrusted resets s and reports true when the stream's remote peer is
// not paired. Every non-pairing stream handler starts with this.
func (g *Guard) rejectUntrusted(ctx context.Context, s network.Stream) (string, bool) {
	pid := s.Conn().RemotePeer()
	deviceID, err := g.DeviceFor(ctx, pid)
	if err != nil {
		_ = s.Reset()
		return "", true
	}
	return deviceID, false
}
