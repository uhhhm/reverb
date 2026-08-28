package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// manifestProtocol carries "what files do you have?". It is the discovery half
// of file sync: /reverb/file/1.0.0 only answers for a path the caller already
// knows, so without this a peer can never learn what to ask for.
const manifestProtocol = "/reverb/manifest/1.0.0"

// A manifest row is ~200 bytes; this leaves room for a library far larger than
// any single device is likely to hold while still bounding the read.
const maxManifestMessageBytes = 64 << 20

// manifestResponse is the served side of manifestProtocol: the rows this
// device authored, i.e. the files it can actually serve.
type manifestResponse struct {
	DeviceID string         `json:"deviceId"`
	Files    []FileManifest `json:"files"`
}

// RegisterManifestHandler serves this device's file manifest to paired peers.
// guard is required for the same reason as the file handler: the manifest is a
// listing of the whole music library and must not go to strangers on the LAN.
func RegisterManifestHandler(h host.Host, store FileStore, localDeviceID string, guard *Guard) {
	h.SetStreamHandler(manifestProtocol, safeHandler("manifest", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(30 * time.Second))
		if store == nil || guard == nil || localDeviceID == "" {
			_ = s.Reset()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, rejected := guard.rejectUntrusted(ctx, s); rejected {
			return
		}
		rows, err := store.ListFileManifests(ctx)
		if err != nil {
			_ = json.NewEncoder(s).Encode(map[string]string{"error": err.Error()})
			return
		}
		resp := manifestResponse{DeviceID: localDeviceID, Files: make([]FileManifest, 0, len(rows))}
		for _, r := range rows {
			// Only advertise our own files. Rows learned from other peers are
			// not ours to serve — the file handler reads from the local music
			// dir, so a foreign row would advertise a path we do not have.
			if r.DeviceID != localDeviceID {
				continue
			}
			resp.Files = append(resp.Files, FileManifest{
				CanonicalID: r.CanonicalID,
				ContentHash: r.ContentHash,
				Size:        r.Size,
				RelPath:     r.RelPath,
				Mtime:       r.Mtime,
				DeviceID:    r.DeviceID,
			})
		}
		if err := json.NewEncoder(s).Encode(resp); err != nil {
			_ = s.Reset()
			return
		}
	}))
}

// RequestManifest asks pid for its file manifest.
func RequestManifest(ctx context.Context, h host.Host, pid peer.ID) (manifestResponse, error) {
	var resp manifestResponse
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := h.NewStream(ctx, pid, manifestProtocol)
	if err != nil {
		return resp, err
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(30 * time.Second))
	_ = s.CloseWrite()
	var wrapper struct {
		DeviceID string         `json:"deviceId"`
		Files    []FileManifest `json:"files"`
		Error    string         `json:"error"`
	}
	if err := decodeLimited(s, maxManifestMessageBytes, &wrapper); err != nil {
		return manifestResponse{}, err
	}
	if wrapper.Error != "" {
		return manifestResponse{}, fmt.Errorf("%s", wrapper.Error)
	}
	resp.DeviceID = wrapper.DeviceID
	resp.Files = wrapper.Files
	return resp, nil
}
