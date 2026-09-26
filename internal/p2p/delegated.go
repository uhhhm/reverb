package p2p

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

// delegatedProtocol carries audio and artwork requests one Device performs
// for another by the catalog id every Device agrees on. Backend ids never
// cross this boundary.
const delegatedProtocol = "/reverb/delegated/1.0.0"

const maxDelegatedHeaderBytes = 64 << 10

type delegatedRequest struct {
	Type      string          `json:"type"`
	CatalogID string          `json:"catalogId"`
	Opts      core.StreamOpts `json:"opts"`
	Range     string          `json:"range,omitempty"`
	Probe     bool            `json:"probe,omitempty"`
	Size      int             `json:"size,omitempty"`
}

type delegatedResponse struct {
	StatusCode    int    `json:"statusCode"`
	ContentType   string `json:"contentType,omitempty"`
	ContentLength int64  `json:"contentLength,omitempty"`
	AcceptRanges  string `json:"acceptRanges,omitempty"`
	ContentRange  string `json:"contentRange,omitempty"`
	Error         string `json:"error,omitempty"`
}

// DelegatedStreamOpener resolves a catalog id against this Device's library
// and opens its bytes. The composition root supplies it so the P2P package
// owns transport without owning library or resolver policy.
type DelegatedStreamOpener func(context.Context, string, core.StreamOpts, string) (core.StreamHandle, error)

// DelegatedCoverOpener resolves a catalog id to its library artwork at the
// requested size. A nil opener makes the handler reject cover requests.
type DelegatedCoverOpener func(context.Context, string, int) (core.CoverArt, error)

// RegisterDelegatedHandler serves library audio and artwork to paired peers only.
func RegisterDelegatedHandler(h host.Host, guard *Guard, open DelegatedStreamOpener, cover DelegatedCoverOpener) {
	registerCompatible(h, delegatedProtocol, safeHandler("delegated", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(30 * time.Second))
		if guard == nil || open == nil {
			_ = s.Reset()
			return
		}
		// The opener must respond promptly, but the returned HTTP body remains
		// bound to this context for the entire song. A fixed 30-second context
		// would truncate otherwise healthy long streams.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		idle := time.AfterFunc(30*time.Second, cancel)
		defer idle.Stop()
		if _, rejected := guard.rejectUntrusted(ctx, s); rejected {
			return
		}
		var req delegatedRequest
		if err := decodeLimited(s, maxFileRequestBytes, &req); err != nil || req.CatalogID == "" || (req.Type != "stream" && req.Type != "cover") {
			_ = json.NewEncoder(s).Encode(delegatedResponse{StatusCode: 400, Error: "invalid delegated request"})
			return
		}
		var handle core.StreamHandle
		var err error
		if req.Type == "cover" {
			if cover == nil || req.Size < 0 || req.Size > 2048 {
				_ = json.NewEncoder(s).Encode(delegatedResponse{StatusCode: 400, Error: "invalid cover request"})
				return
			}
			var art core.CoverArt
			art, err = cover(ctx, req.CatalogID, req.Size)
			handle = core.StreamHandle{Body: art.Body, ContentType: art.ContentType, StatusCode: 200}
		} else {
			handle, err = open(ctx, req.CatalogID, req.Opts, req.Range)
		}
		if err != nil {
			status := 502
			if errors.Is(err, core.ErrLibraryItemNotFound) {
				status = 404
			}
			_ = json.NewEncoder(s).Encode(delegatedResponse{StatusCode: status, Error: err.Error()})
			return
		}
		if handle.Body == nil {
			_ = json.NewEncoder(s).Encode(delegatedResponse{StatusCode: 404, Error: "track unavailable"})
			return
		}
		defer handle.Body.Close()
		status := handle.StatusCode
		if status == 0 {
			status = 200
		}
		resp := delegatedResponse{
			StatusCode: status, ContentType: handle.ContentType, ContentLength: handle.ContentLength,
			AcceptRanges: handle.AcceptRanges, ContentRange: handle.ContentRange,
		}
		if err := json.NewEncoder(s).Encode(resp); err != nil {
			return
		}
		if req.Probe {
			return
		}
		idle.Reset(idleTransferTimeout)
		if _, err := copyStreamIdle(s, &delegatedIdleReader{Reader: handle.Body, timer: idle}, s); err != nil {
			_ = s.Reset()
		}
	}))
}

// RequestDelegatedStream opens one authenticated-by-pairing stream from pid.
// The returned body owns the libp2p stream and must be closed by the caller.
func RequestDelegatedStream(ctx context.Context, h host.Host, pid peer.ID, catalogID string, opts core.StreamOpts, byteRange string, probe bool) (core.StreamHandle, error) {
	return requestDelegated(ctx, h, pid, delegatedRequest{Type: "stream", CatalogID: catalogID, Opts: opts, Range: byteRange, Probe: probe})
}

// RequestDelegatedCover fetches catalog artwork from pid. The returned body owns
// the libp2p stream and must be closed by the caller.
func RequestDelegatedCover(ctx context.Context, h host.Host, pid peer.ID, catalogID string, size int) (core.CoverArt, error) {
	handle, err := requestDelegated(ctx, h, pid, delegatedRequest{Type: "cover", CatalogID: catalogID, Size: size})
	if err != nil {
		return core.CoverArt{}, err
	}
	return core.CoverArt{Body: handle.Body, ContentType: handle.ContentType}, nil
}

func requestDelegated(ctx context.Context, h host.Host, pid peer.ID, req delegatedRequest) (core.StreamHandle, error) {
	if h == nil || req.CatalogID == "" {
		return core.StreamHandle{}, fmt.Errorf("delegated stream unavailable")
	}
	s, err := h.NewStream(ctx, pid, SupportedProtocols(delegatedProtocol)...)
	if err != nil {
		return core.StreamHandle{}, err
	}
	_ = s.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(s).Encode(req); err != nil {
		_ = s.Reset()
		return core.StreamHandle{}, err
	}
	_ = s.CloseWrite()
	reader := bufio.NewReaderSize(s, 4096)
	header := make([]byte, 0, 256)
	for len(header) <= maxDelegatedHeaderBytes {
		b, err := reader.ReadByte()
		if err != nil {
			_ = s.Reset()
			return core.StreamHandle{}, err
		}
		header = append(header, b)
		if b == '\n' {
			break
		}
	}
	if len(header) > maxDelegatedHeaderBytes {
		_ = s.Reset()
		return core.StreamHandle{}, fmt.Errorf("delegated response header too large")
	}
	var resp delegatedResponse
	if err := json.Unmarshal(header, &resp); err != nil {
		_ = s.Reset()
		return core.StreamHandle{}, err
	}
	if resp.Error != "" || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = s.Close()
		if resp.Error == "" {
			resp.Error = "delegated stream failed"
		}
		return core.StreamHandle{}, fmt.Errorf("%s (status %d)", resp.Error, resp.StatusCode)
	}
	if req.Probe {
		_ = s.Close()
		return core.StreamHandle{StatusCode: resp.StatusCode, ContentType: resp.ContentType, ContentLength: resp.ContentLength, AcceptRanges: resp.AcceptRanges, ContentRange: resp.ContentRange}, nil
	}
	_ = s.SetDeadline(time.Time{})
	return core.StreamHandle{
		Body:          &delegatedBody{Reader: reader, stream: s},
		StatusCode:    resp.StatusCode,
		ContentType:   resp.ContentType,
		ContentLength: resp.ContentLength,
		AcceptRanges:  resp.AcceptRanges,
		ContentRange:  resp.ContentRange,
	}, nil
}

type delegatedBody struct {
	io.Reader
	stream network.Stream
}

func (b *delegatedBody) Close() error { return b.stream.Close() }

type delegatedIdleReader struct {
	io.Reader
	timer *time.Timer
}

func (r *delegatedIdleReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 {
		r.timer.Reset(idleTransferTimeout)
	}
	return n, err
}

// DelegatorStore is the persisted routing knowledge a Delegated request uses.
// The device row identifies the always-on Server; peer rows provide libp2p
// identities and the last time each was reached.
type DelegatorStore interface {
	ListDevices(context.Context) ([]db.Device, error)
	ListTrustedPeers(context.Context) ([]db.P2pPeer, error)
}

// Delegator selects a paired Device and performs Delegated requests. Providers
// are used because the host and guard start after the HTTP server is built.
type Delegator struct {
	host  func() host.Host
	guard func() *Guard
	store DelegatorStore
	now   func() time.Time

	mu sync.Mutex
	// playable remembers recent probe answers per catalog id, and
	// unreachableUntil short-circuits probes while no paired device answers,
	// so browsing a large catalogue does not dial once per row per refresh.
	playable         map[string]playableProbe
	unreachableUntil time.Time
}

// playableProbe is a cached answer. A yes names the device that gave it and
// holds only while that device stays connected.
type playableProbe struct {
	ok   bool
	at   time.Time
	peer peer.ID
}

const (
	playableProbeTTL    = 5 * time.Minute
	unplayableProbeTTL  = 30 * time.Second
	unreachableProbeTTL = 30 * time.Second
)

// errNoReachablePeer means no paired device could be dialled; errNoPairedPeer,
// which wraps it, means none is paired at all.
var (
	errNoReachablePeer = errors.New("no paired device is reachable")
	errNoPairedPeer    = fmt.Errorf("no device is paired: %w", errNoReachablePeer)
)

func NewDelegator(hostProvider func() host.Host, guardProvider func() *Guard, store DelegatorStore) *Delegator {
	return &Delegator{host: hostProvider, guard: guardProvider, store: store, now: time.Now, playable: map[string]playableProbe{}}
}

// eachPeer calls try on each reachable paired device, Server first, until one
// succeeds, and marks that device as reached. It returns errNoReachablePeer
// when no device could be dialled, else the joined per-device failures, in
// which an undialled device appears as errNoReachablePeer.
func (d *Delegator) eachPeer(ctx context.Context, try func(host.Host, peer.ID) error) error {
	if d == nil || d.host == nil || d.guard == nil || d.store == nil {
		return errors.New("delegated transport unavailable")
	}
	h, guard := d.host(), d.guard()
	if h == nil || guard == nil {
		return errors.New("delegated transport unavailable")
	}
	candidates, err := d.candidates(ctx)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return errNoPairedPeer
	}
	var failures []error
	tried := false
	for _, pid := range candidates {
		if !EnsureConnected(ctx, h, guard, pid) {
			failures = append(failures, fmt.Errorf("device %s: %w", pid, errNoReachablePeer))
			continue
		}
		tried = true
		err := try(h, pid)
		if err == nil {
			guard.Touch(ctx, pid)
			return nil
		}
		failures = append(failures, fmt.Errorf("device %s: %w", pid, err))
	}
	if !tried {
		return errNoReachablePeer
	}
	return errors.Join(failures...)
}

func (d *Delegator) Stream(ctx context.Context, catalogID string, opts core.StreamOpts, byteRange string) (core.StreamHandle, error) {
	return d.request(ctx, catalogID, opts, byteRange, false)
}

// Playable reports whether some paired device can stream catalogID. Answers
// are cached briefly, and a yes is dropped once its device disconnects;
// Stream itself always asks the network.
func (d *Delegator) Playable(ctx context.Context, catalogID string) bool {
	if d == nil {
		return false
	}
	now := d.now()
	d.mu.Lock()
	if now.Before(d.unreachableUntil) {
		d.mu.Unlock()
		return false
	}
	if probe, ok := d.playable[catalogID]; ok && probe.fresh(now) && (!probe.ok || d.connected(probe.peer)) {
		d.mu.Unlock()
		return probe.ok
	}
	d.mu.Unlock()

	var answeredBy peer.ID
	err := d.eachPeer(ctx, func(h host.Host, pid peer.ID) error {
		handle, err := RequestDelegatedStream(ctx, h, pid, catalogID, core.StreamOpts{}, "", true)
		if handle.Body != nil {
			_ = handle.Body.Close()
		}
		if err == nil {
			answeredBy = pid
		}
		return err
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case err == nil:
	case (err == errNoReachablePeer || err == errNoPairedPeer) && !errors.Is(ctx.Err(), context.Canceled):
		// No device could be dialled (a joined error means some device was),
		// including a dial that stalled into the caller's deadline, the usual
		// sign of a sleeping or distant device.
		d.unreachableUntil = now.Add(unreachableProbeTTL)
		return false
	case ctx.Err() != nil:
		return false // cut short, which says nothing about this track
	}
	for id, probe := range d.playable {
		if !probe.fresh(now) {
			delete(d.playable, id)
		}
	}
	d.playable[catalogID] = playableProbe{ok: err == nil, at: now, peer: answeredBy}
	return err == nil
}

// fresh reports whether the answer may still be used; a no expires sooner, as
// it is often a device still starting or a transient failure.
func (p playableProbe) fresh(now time.Time) bool {
	if p.ok {
		return now.Sub(p.at) < playableProbeTTL
	}
	return now.Sub(p.at) < unplayableProbeTTL
}

func (d *Delegator) connected(pid peer.ID) bool {
	h := d.host()
	return h != nil && h.Network().Connectedness(pid) == network.Connected
}

// Cover fetches catalogID's artwork from the first paired device that has it.
func (d *Delegator) Cover(ctx context.Context, catalogID string, size int) (core.CoverArt, error) {
	var art core.CoverArt
	err := d.eachPeer(ctx, func(h host.Host, pid peer.ID) error {
		var err error
		art, err = RequestDelegatedCover(ctx, h, pid, catalogID, size)
		return err
	})
	return art, err
}

func (d *Delegator) request(ctx context.Context, catalogID string, opts core.StreamOpts, byteRange string, probe bool) (core.StreamHandle, error) {
	var handle core.StreamHandle
	err := d.eachPeer(ctx, func(h host.Host, pid peer.ID) error {
		var err error
		handle, err = RequestDelegatedStream(ctx, h, pid, catalogID, opts, byteRange, probe)
		return err
	})
	return handle, err
}

func (d *Delegator) candidates(ctx context.Context) ([]peer.ID, error) {
	devices, err := d.store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	serverIDs := make(map[string]bool)
	for _, device := range devices {
		if device.IsServer == 1 {
			serverIDs[device.ID] = true
		}
	}
	rows, err := d.store.ListTrustedPeers(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		iServer := rows[i].DeviceID.Valid && serverIDs[rows[i].DeviceID.String]
		jServer := rows[j].DeviceID.Valid && serverIDs[rows[j].DeviceID.String]
		if iServer != jServer {
			return iServer
		}
		if rows[i].LastSeen == rows[j].LastSeen {
			return rows[i].PeerID < rows[j].PeerID
		}
		return rows[i].LastSeen > rows[j].LastSeen
	})
	out := make([]peer.ID, 0, len(rows))
	for _, row := range rows {
		pid, err := peer.Decode(row.PeerID)
		if err == nil {
			out = append(out, pid)
		}
	}
	return out, nil
}
