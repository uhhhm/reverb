package offlineset

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// PendingUpload is a Download made on this device that no paired device has
// confirmed it holds yet. It is never pruned while pending.
type PendingUpload struct {
	RelPath   string `json:"relPath"`
	Title     string `json:"title"`
	Artist    string `json:"artist"`
	Album     string `json:"album,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	// DownloadedAt is when it was recorded, in Unix milliseconds.
	DownloadedAt int64 `json:"downloadedAt"`
}

// AddPending records a Download that landed at file, an absolute path in the
// music folder or one relative to it. A path outside the folder, or a
// directory (a downloader that names no file), is rejected so the download
// cannot be reported complete without a durable pending-upload row.
func (k *Keeper) AddPending(ctx context.Context, file string) error {
	rel := file
	if filepath.IsAbs(file) {
		r, err := filepath.Rel(k.cfg.MusicDir, file)
		if err != nil {
			return err
		}
		rel = r
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		return fmt.Errorf("offline set: download output %s is a directory, not a file", file)
	}
	if rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return fmt.Errorf("offline set: %s is not in the music folder", file)
	}
	info, err := os.Stat(filepath.Join(k.cfg.MusicDir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("offline set: download output %s is a directory, not a file", file)
	}
	return k.cfg.Store.UpsertPendingUpload(ctx, dbPending(rel, k.cfg.Now().UnixMilli()))
}

// PendingUploads lists the Downloads still waiting for a paired device, oldest
// first, with what their tags say once file sync has read them.
func (k *Keeper) PendingUploads(ctx context.Context) ([]PendingUpload, error) {
	rows, err := k.cfg.Store.ListPendingUploads(ctx)
	if err != nil {
		return nil, err
	}
	sizes, err := k.localManifestFiles(ctx)
	if err != nil {
		return nil, err
	}
	locals, err := k.taggedLocalFiles(ctx, sizes)
	if err != nil {
		return nil, err
	}
	out := make([]PendingUpload, 0, len(rows))
	for _, r := range rows {
		p := PendingUpload{RelPath: r.RelPath, DownloadedAt: r.DownloadedAt}
		if f, ok := locals[r.RelPath]; ok {
			p.Title, p.Artist, p.Album, p.SizeBytes = f.title, f.artist, f.album, f.size
		} else if f, ok := sizes[r.RelPath]; ok {
			p.SizeBytes = f.size
		} else if info, err := os.Stat(filepath.Join(k.cfg.MusicDir, filepath.FromSlash(r.RelPath))); err == nil {
			p.SizeBytes = info.Size()
		}
		if p.Title == "" {
			p.Title = strings.TrimSuffix(path.Base(r.RelPath), path.Ext(r.RelPath))
		}
		out = append(out, p)
	}
	return out, nil
}

// heldByPeers is the content every paired device offered when last reached.
func (k *Keeper) heldByPeers() map[string]bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	held := map[string]bool{}
	for _, files := range k.offered {
		for _, f := range files {
			held[f.ContentHash] = true
		}
	}
	return held
}

// settlePending resolves the pending uploads a peer now holds: one an offline
// playlist names becomes the offline set's, and the rest are removed. It
// returns the paths still pending, which nothing may prune, and whether it
// removed any file.
func (k *Keeper) settlePending(ctx context.Context, root *os.Root, keep map[string]bool) (map[string]bool, bool, error) {
	rows, err := k.cfg.Store.ListPendingUploads(ctx)
	if err != nil || len(rows) == 0 {
		return nil, false, err
	}
	local, err := k.localManifestFiles(ctx)
	if err != nil {
		return nil, false, err
	}
	held := k.heldByPeers()
	protect := map[string]bool{}
	removed := false
	for _, r := range rows {
		// The manifest can outlive a file, so the disk says whether it is gone.
		if _, err := root.Stat(filepath.FromSlash(r.RelPath)); errors.Is(err, fs.ErrNotExist) {
			_ = k.cfg.Store.DeletePendingUpload(ctx, r.RelPath)
			continue
		}
		cur, scanned := local[r.RelPath]
		if !scanned {
			// Not hashed yet: it waits for the scan.
			protect[r.RelPath] = true
			continue
		}
		if !held[cur.hash] {
			protect[r.RelPath] = true
			continue
		}
		if keep[r.RelPath] {
			// Uploaded, and wanted offline: the offline set prunes it once no
			// offline playlist names it.
			if err := k.cfg.Store.UpsertOfflineFile(ctx, dbOffline(r.RelPath, cur.hash, k.cfg.Now().UnixMilli())); err != nil {
				return nil, removed, err
			}
		} else {
			if err := root.Remove(filepath.FromSlash(r.RelPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				protect[r.RelPath] = true
				continue
			}
			removed = true
			removeEmptyParents(root, r.RelPath)
		}
		_ = k.cfg.Store.DeletePendingUpload(ctx, r.RelPath)
	}
	return protect, removed, nil
}

// removeEmptyParents removes the directories rel leaves empty.
func removeEmptyParents(root *os.Root, rel string) {
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if root.Remove(filepath.FromSlash(dir)) != nil {
			return
		}
	}
}
