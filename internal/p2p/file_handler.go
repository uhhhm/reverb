package p2p

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

// fileRequest is sent on /reverb/file/1.0.0 to fetch a file by rel path or hash.
type fileRequest struct {
	RelPath     string `json:"relPath,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

// RegisterFileHandler serves files from musicDir on /reverb/file/1.0.0.
func RegisterFileHandler(h host.Host, musicDir string) {
	h.SetStreamHandler("/reverb/file/1.0.0", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(60 * time.Second))
		if musicDir == "" {
			_ = s.Reset()
			return
		}
		var req fileRequest
		if err := json.NewDecoder(s).Decode(&req); err != nil {
			_ = s.Reset()
			return
		}
		var path string
		if req.RelPath != "" {
			// Clean relPath and reject absolute or traversal.
			cleanRel := filepath.Clean(filepath.FromSlash(req.RelPath))
			if filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
				_ = s.Reset()
				return
			}
			path = filepath.Join(musicDir, cleanRel)
		} else {
			_ = s.Reset()
			return
		}
		// Ensure path is within musicDir (defense in depth).
		rel, err := filepath.Rel(musicDir, path)
		if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
			_ = s.Reset()
			return
		}
		f, err := os.Open(path)
		if err != nil {
			_ = s.Reset()
			return
		}
		defer f.Close()
		if _, err := io.Copy(s, f); err != nil {
			_ = s.Reset()
			return
		}
	})
}
