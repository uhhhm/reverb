package p2p

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// coverProtocol carries user-uploaded album and track artwork. The sync log
// replicates only the address of a cover — its content hash — because a change
// log is not a place to put images; this is how the bytes follow.
const coverProtocol = "/reverb/cover/1.0.0"

// maxCoverBlobBytes bounds one transfer. It matches the upload limit, so a
// cover that could be stored locally can also be replicated.
const maxCoverBlobBytes = 5 << 20

// coverRequest names one blob by content hash. There is no path: the hash is
// the whole address, which is what makes serving one safe.
type coverRequest struct {
	Sha256 string `json:"sha256"`
	Ext    string `json:"ext"`
}

// validCoverRef checks that a hash and extension are exactly the shape the
// cover store produces. This is the only place a value off the wire becomes a
// file name, so it is the path-traversal defence.
func validCoverRef(sha, ext string) bool {
	if len(sha) != 64 {
		return false
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return false
	}
	switch ext {
	case "jpg", "png", "webp":
		return true
	}
	return false
}

func coverBlobPath(dir, sha, ext string) string {
	return filepath.Join(dir, sha+"."+ext)
}

// RegisterCoverHandler serves uploaded artwork from coverDir to paired peers
// only. guard is required: without it any dialer could read this device's
// covers.
func RegisterCoverHandler(h host.Host, coverDir string, guard *Guard) {
	h.SetStreamHandler(coverProtocol, safeHandler("cover", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(30 * time.Second))
		if coverDir == "" || guard == nil {
			_ = s.Reset()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, rejected := guard.rejectUntrusted(ctx, s); rejected {
			return
		}
		var req coverRequest
		if err := decodeLimited(s, maxFileRequestBytes, &req); err != nil {
			_ = s.Reset()
			return
		}
		if !validCoverRef(req.Sha256, req.Ext) {
			_ = s.Reset()
			return
		}
		f, err := os.Open(coverBlobPath(coverDir, req.Sha256, req.Ext))
		if err != nil {
			_ = s.Reset()
			return
		}
		defer f.Close()
		if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
			_ = s.Reset()
			return
		}
		if _, err := io.Copy(s, io.LimitReader(f, maxCoverBlobBytes)); err != nil {
			_ = s.Reset()
		}
	}))
}

// FetchCover downloads one blob from a peer into coverDir. The bytes are hashed
// on the way in and rejected unless they are what was asked for, so a peer
// cannot answer one cover request with a different image.
func FetchCover(ctx context.Context, h host.Host, coverDir, peerID, sha, ext string) error {
	if h == nil || coverDir == "" {
		return fmt.Errorf("p2p cover: not configured")
	}
	if !validCoverRef(sha, ext) {
		return fmt.Errorf("p2p cover: invalid reference %q.%q", sha, ext)
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		return err
	}
	s, err := h.NewStream(ctx, pid, coverProtocol)
	if err != nil {
		return err
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(30 * time.Second))
	if err := json.NewEncoder(s).Encode(coverRequest{Sha256: sha, Ext: ext}); err != nil {
		return err
	}
	_ = s.CloseWrite()

	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(coverDir, ".reverb-cover-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	digest := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, digest), io.LimitReader(s, maxCoverBlobBytes+1))
	_ = tmp.Close()
	if copyErr != nil || n > maxCoverBlobBytes || n == 0 {
		_ = os.Remove(tmpPath)
		if copyErr != nil {
			return copyErr
		}
		return fmt.Errorf("p2p cover: %s is empty or over the size limit", sha)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != sha {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("p2p cover: hash mismatch, expected %s got %s", sha, got)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, coverBlobPath(coverDir, sha, ext))
}
