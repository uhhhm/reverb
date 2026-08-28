package p2p

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

// fileRequest is sent on /reverb/file/1.0.0 to fetch a file by rel path or hash.
type fileRequest struct {
	RelPath     string `json:"relPath,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

// RegisterFileHandler serves files from musicDir on /reverb/file/1.0.0 to
// paired peers only. guard is required: without it the handler would hand any
// file under musicDir to any dialer on the LAN or via DHT/relay.
func RegisterFileHandler(h host.Host, musicDir string, guard *Guard) {
	h.SetStreamHandler("/reverb/file/1.0.0", safeHandler("file", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(60 * time.Second))
		if musicDir == "" || guard == nil {
			_ = s.Reset()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, rejected := guard.rejectUntrusted(ctx, s); rejected {
			return
		}
		var req fileRequest
		if err := decodeLimited(s, maxFileRequestBytes, &req); err != nil {
			_ = s.Reset()
			return
		}
		if req.RelPath == "" {
			_ = s.Reset()
			return
		}
		cleanRel, err := validateRelPath(req.RelPath)
		if err != nil {
			_ = s.Reset()
			return
		}
		// os.OpenRoot confines resolution to musicDir: unlike a lexical
		// filepath.Rel check it will not follow a symlink out of the tree.
		root, err := os.OpenRoot(musicDir)
		if err != nil {
			_ = s.Reset()
			return
		}
		defer root.Close()
		f, err := root.Open(cleanRel)
		if err != nil {
			_ = s.Reset()
			return
		}
		defer f.Close()
		// Refuse anything that is not a regular file: a FIFO or device node
		// under musicDir would otherwise block or stream indefinitely.
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			_ = s.Reset()
			return
		}
		if _, err := io.Copy(s, f); err != nil {
			_ = s.Reset()
			return
		}
	}))
}
