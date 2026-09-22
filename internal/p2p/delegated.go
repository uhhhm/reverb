package p2p

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
)

// delegatedProtocol carries requests one Device performs for another. Version
// 1 has one request: stream a library track by the catalog id every Device
// agrees on. Backend ids never cross this boundary.
const delegatedProtocol = "/reverb/delegated/1.0.0"

const maxDelegatedHeaderBytes = 64 << 10

type delegatedRequest struct {
	Type      string          `json:"type"`
	CatalogID string          `json:"catalogId"`
	Opts      core.StreamOpts `json:"opts"`
	Range     string          `json:"range,omitempty"`
	Probe     bool            `json:"probe,omitempty"`
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

// RegisterDelegatedHandler serves library audio to paired peers only.
func RegisterDelegatedHandler(h host.Host, guard *Guard, open DelegatedStreamOpener) {
	h.SetStreamHandler(delegatedProtocol, safeHandler("delegated", func(s network.Stream) {
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
		if err := decodeLimited(s, maxFileRequestBytes, &req); err != nil || req.Type != "stream" || req.CatalogID == "" {
			_ = json.NewEncoder(s).Encode(delegatedResponse{StatusCode: 400, Error: "invalid delegated request"})
			return
		}
		handle, err := open(ctx, req.CatalogID, req.Opts, req.Range)
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
	if h == nil || catalogID == "" {
		return core.StreamHandle{}, fmt.Errorf("delegated stream unavailable")
	}
	s, err := h.NewStream(ctx, pid, delegatedProtocol)
	if err != nil {
		return core.StreamHandle{}, err
	}
	_ = s.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(s).Encode(delegatedRequest{Type: "stream", CatalogID: catalogID, Opts: opts, Range: byteRange, Probe: probe}); err != nil {
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
	if probe {
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
}

func NewDelegator(hostProvider func() host.Host, guardProvider func() *Guard, store DelegatorStore) *Delegator {
	return &Delegator{host: hostProvider, guard: guardProvider, store: store}
}

func (d *Delegator) Stream(ctx context.Context, catalogID string, opts core.StreamOpts, byteRange string) (core.StreamHandle, error) {
	return d.request(ctx, catalogID, opts, byteRange, false)
}

func (d *Delegator) Playable(ctx context.Context, catalogID string) bool {
	handle, err := d.request(ctx, catalogID, core.StreamOpts{}, "", true)
	if handle.Body != nil {
		_ = handle.Body.Close()
	}
	return err == nil
}

func (d *Delegator) request(ctx context.Context, catalogID string, opts core.StreamOpts, byteRange string, probe bool) (core.StreamHandle, error) {
	if d == nil || d.host == nil || d.guard == nil || d.store == nil {
		return core.StreamHandle{}, fmt.Errorf("delegated stream unavailable")
	}
	h, guard := d.host(), d.guard()
	if h == nil || guard == nil {
		return core.StreamHandle{}, fmt.Errorf("delegated stream unavailable")
	}
	candidates, err := d.candidates(ctx)
	if err != nil {
		return core.StreamHandle{}, err
	}
	var failures []error
	for _, pid := range candidates {
		if !EnsureConnected(ctx, h, guard, pid) {
			failures = append(failures, fmt.Errorf("device %s is unreachable", pid))
			continue
		}
		handle, err := RequestDelegatedStream(ctx, h, pid, catalogID, opts, byteRange, probe)
		if err == nil {
			guard.Touch(ctx, pid)
			return handle, nil
		}
		failures = append(failures, fmt.Errorf("device %s: %w", pid, err))
	}
	if len(failures) == 0 {
		return core.StreamHandle{}, fmt.Errorf("no paired device is reachable")
	}
	return core.StreamHandle{}, errors.Join(failures...)
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
