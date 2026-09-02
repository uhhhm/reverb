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
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/store/db"
)

// FileStore is the minimal seam for file_manifest.
type FileStore interface {
	UpsertFileManifest(ctx context.Context, arg db.UpsertFileManifestParams) error
	ListFileManifests(ctx context.Context) ([]db.FileManifest, error)
	DeleteFileManifest(ctx context.Context, canonicalID string) error
}

const maxFileHashSize = 2 * 1024 * 1024 * 1024 // 2 GiB — skip larger files to bound hashing

func validateRelPath(relPath string) (string, error) {
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if cleanRel == "." || cleanRel == "" || filepath.IsAbs(cleanRel) || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid relPath: %q", relPath)
	}
	return cleanRel, nil
}

// FileSyncer watches REVERB_DOWNLOAD_DIR and keeps file_manifest in sync.
// Full replication: every file under the music dir is hashed (sha256) and
// advertised via /reverb/file/1.0.0 want/have. Peer fetch is Phase 4 final.
type FileSyncer struct {
	store    FileStore
	deviceID string
	musicDir string
}

func NewFileSyncer(store FileStore, deviceID, musicDir string) *FileSyncer {
	return &FileSyncer{store: store, deviceID: deviceID, musicDir: musicDir}
}

// ScanAndSync hashes every regular file under musicDir and upserts manifest.
// It is idempotent and safe to run periodically (5min) and on fsnotify.
// Optimization: skips hashing when mtime+size unchanged, deletes stale entries.
// Per-device canonical_id (deviceID:relPath) prevents cross-device overwrites
// (same relPath on different devices keeps both hashes, dedup by hash elsewhere).
func (f *FileSyncer) ScanAndSync(ctx context.Context) error {
	if f == nil || f.store == nil || f.musicDir == "" {
		return nil
	}
	// Build existing map for fast mtime/size short-circuit and delete detection.
	existing, err := f.store.ListFileManifests(ctx)
	if err != nil {
		return err
	}
	existingByID := make(map[string]db.FileManifest, len(existing))
	for _, m := range existing {
		existingByID[m.CanonicalID] = m
	}
	seen := make(map[string]bool)
	// Also track seen relPaths for migration of old unprefixed rows.
	seenRel := make(map[string]bool)
	// A walk error on any entry means the file set this scan produces is
	// incomplete, so the stale-delete pass below must not run: a music dir that
	// is briefly unreadable — an unmounted external or network volume — would
	// otherwise delete every manifest this device advertises and force a full
	// re-hash when the volume comes back.
	incomplete := false
	walkErr := filepath.WalkDir(f.musicDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			incomplete = true
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip symlinks to avoid loops.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, _ := filepath.Rel(f.musicDir, path)
		relSlash := filepath.ToSlash(rel)
		// Per-device canonical ID prevents cross-device overwrites.
		canonicalID := relSlash
		if f.deviceID != "" {
			canonicalID = f.deviceID + ":" + relSlash
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size := info.Size()
		if size > maxFileHashSize {
			// Skip unbounded files to bound hashing work.
			return nil
		}
		mtime := info.ModTime().UnixMilli()
		seen[canonicalID] = true
		seenRel[relSlash] = true
		if prev, ok := existingByID[canonicalID]; ok && prev.Mtime == mtime && prev.Size == size && prev.DeviceID == f.deviceID {
			// Unchanged — skip hashing.
			return nil
		}
		h := sha256.New()
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		// Bound hashing to file size to avoid unbounded reads.
		if _, err := io.Copy(h, io.LimitReader(file, size+1)); err != nil {
			_ = file.Close()
			return nil
		}
		_ = file.Close()
		if err := ctx.Err(); err != nil {
			return err
		}
		hash := hex.EncodeToString(h.Sum(nil))
		if err := f.store.UpsertFileManifest(ctx, db.UpsertFileManifestParams{
			CanonicalID: canonicalID,
			ContentHash: hash,
			Size:        size,
			RelPath:     relSlash,
			Mtime:       mtime,
			DeviceID:    f.deviceID,
		}); err != nil {
			// Don't fail whole scan on single upsert error.
			return nil
		}
		return nil
	})
	if walkErr != nil {
		// Context cancellation should propagate, but stale-delete must not run
		// if walk was aborted — the file set is incomplete.
		if err := ctx.Err(); err != nil {
			return err
		}
		return walkErr
	}
	// An unmounted volume usually leaves its mount point behind as an empty
	// directory on the boot disk, so the walk succeeds and finds nothing. A
	// library that went from many files to none is a mount that went away far
	// more often than a library the user emptied; keep the manifests either way,
	// since a stale row costs a failed fetch and a wrongly deleted one costs the
	// whole library.
	if len(seen) == 0 && f.hasOwnManifests(existingByID) {
		incomplete = true
	}
	if incomplete {
		return nil
	}
	// Delete stale manifests for files that no longer exist (only for this device).
	for id, m := range existingByID {
		if m.DeviceID != f.deviceID {
			continue
		}
		if seen[id] {
			continue
		}
		// Migration: old rows used unprefixed canonical_id == rel_path.
		// If we just migrated to prefixed IDs, the old row's relSlash is still
		// present in seenRel — treat it as seen and delete the old unprefixed row
		// to avoid orphaned duplicates (old and new coexist for one scan).
		if m.CanonicalID == m.RelPath && seenRel[m.RelPath] {
			_ = f.store.DeleteFileManifest(ctx, id)
			continue
		}
		if !seen[id] {
			_ = f.store.DeleteFileManifest(ctx, id)
		}
	}
	return nil
}

// hasOwnManifests reports whether this device already advertises any file.
func (f *FileSyncer) hasOwnManifests(existing map[string]db.FileManifest) bool {
	for _, m := range existing {
		if m.DeviceID == f.deviceID {
			return true
		}
	}
	return false
}

// Run starts periodic scanning (5m) plus fsnotify immediate sync until ctx canceled.
func (f *FileSyncer) Run(ctx context.Context) {
	if f == nil {
		return
	}
	_ = f.ScanAndSync(ctx)
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()

	// fsnotify watcher for immediate file changes (debounced 1s).
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		defer watcher.Close()
		// Ensure musicDir exists before watching.
		_ = os.MkdirAll(f.musicDir, 0o755)
		_ = watcher.Add(f.musicDir)
		// Also watch subdirs as they appear; we add them on create.
		SafeGo("file watcher", func() {
			debounce := time.NewTimer(0)
			if !debounce.Stop() {
				<-debounce.C
			}
			defer debounce.Stop()
			pending := false
			for {
				select {
				case <-ctx.Done():
					return
				case ev, ok := <-watcher.Events:
					if !ok {
						return
					}
					// Add new directories to watcher.
					if ev.Op&fsnotify.Create == fsnotify.Create {
						if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
							_ = watcher.Add(ev.Name)
						}
					}
					if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
						// Debounce: wait 1s after last event before scanning.
						pending = true
						if !debounce.Stop() {
							select {
							case <-debounce.C:
							default:
							}
						}
						debounce.Reset(1 * time.Second)
					}
					_ = pending
				case err, ok := <-watcher.Errors:
					if !ok {
						return
					}
					_ = err
				case <-debounce.C:
					if pending {
						pending = false
						_ = f.ScanAndSync(ctx)
					}
				}
			}
		})
		// Recursively add existing subdirs to watcher.
		_ = filepath.WalkDir(f.musicDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			_ = watcher.Add(path)
			return nil
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = f.ScanAndSync(ctx)
		}
	}
}

// FetchFileViaPeer fetches a file by relPath from a peer via /reverb/file/1.0.0
// and writes it into musicDir.
//
// expectedHash is mandatory: the serving peer chooses the bytes it returns, so
// without a hash to check them against there is nothing to distinguish a real
// file from a substituted one. The transfer lands in a temp file alongside the
// destination and is renamed into place only after the digest matches, so a
// failed or rejected fetch can never truncate or replace a good local file.
func (f *FileSyncer) FetchFileViaPeer(ctx context.Context, h host.Host, peerIDStr, relPath string, expectedHash ...string) error {
	if f == nil || h == nil {
		return fmt.Errorf("nil syncer or host")
	}
	// Validate relPath before any network I/O (saves dial) and before FS mutation.
	cleanRel, err := validateRelPath(relPath)
	if err != nil {
		return err
	}
	if f.musicDir == "" {
		return fmt.Errorf("musicDir not configured")
	}
	expHash := ""
	if len(expectedHash) > 0 {
		expHash = expectedHash[0]
	}
	if expHash == "" {
		return fmt.Errorf("contentHash is required to fetch %q", relPath)
	}
	dstPath := filepath.Join(f.musicDir, cleanRel)
	// Defense in depth: ensure dstPath is within musicDir.
	rel, err := filepath.Rel(f.musicDir, dstPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid relPath: %q", relPath)
	}
	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		return err
	}
	s, err := h.NewStream(ctx, pid, "/reverb/file/1.0.0")
	if err != nil {
		return err
	}
	defer s.Close()
	// Request and response header only; copyStreamIdle re-arms the deadline
	// per chunk for the body, which may be gigabytes over a slow link.
	_ = s.SetDeadline(time.Now().Add(60 * time.Second))
	if err := json.NewEncoder(s).Encode(fileRequest{RelPath: relPath, ContentHash: expHash}); err != nil {
		return err
	}
	_ = s.CloseWrite()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), ".reverb-fetch-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	// Hash while copying so the file is read once.
	digest := sha256.New()
	n, copyErr := copyStreamIdle(io.MultiWriter(tmp, digest), io.LimitReader(s, maxFileBytes), s)
	if copyErr != nil {
		cleanup()
		return copyErr
	}
	if n >= maxFileBytes {
		cleanup()
		return fmt.Errorf("file %q exceeds %d byte limit", relPath, int64(maxFileBytes))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != expHash {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("hash mismatch for %q: expected %s got %s", relPath, expHash, got)
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	// After fetch, re-hash and upsert manifest.
	return f.ScanAndSync(ctx)
}
