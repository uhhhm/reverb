// Package portablemigrate makes an existing library replicable to a device
// whose filesystem is narrower than the one that built it.
//
// Portable naming governs names minted from the moment it lands. An owner who
// has been running Reverb on Linux or macOS already has tracks on disk whose
// names Windows cannot store, and those stay permanently stuck: the pull lane
// reports them as unfetchable, which makes the failure visible but cannot
// resolve it. Pairing a Windows device to an established library is exactly the
// case this product is for, so this renames what is already there onto the same
// portable form new downloads receive.
//
// This touches files in the owner's own music folder, which is the most
// destructive thing Reverb does to data it did not create. Two properties are
// therefore structural rather than incidental:
//
//   - Interruptible. A file moves by linking it to its new name and then
//     unlinking the old one, so an interruption leaves it readable under one
//     name or, at worst, under both — the same bytes twice, never a partial
//     file and never none. Nothing is journalled beforehand, and the work to
//     do is derived from what is on disk, so rerunning finishes the job and
//     tidies a duplicate away. The manifest is repaired the same way, from
//     disk rather than from a record of what the last run did, since no such
//     record survives a crash.
//   - Non-destructive. A target name that is already taken is resolved to a
//     free one, and the move itself refuses a destination that appears in the
//     meantime. No rename ever overwrites or removes another file.
package portablemigrate

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uhhhm/reverb/internal/portablename"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Rename is one file or directory this migration moved, in slash-relative form
// so it reads the same on every platform.
type Rename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Failure is one entry the migration could not move, and why. A single
// unmovable file — one held open by another process — must not abandon the rest
// of the library, so failures are collected rather than returned as an error.
type Failure struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Result is what the owner is shown after a run.
type Result struct {
	Renamed  []Rename  `json:"renamed"`
	Failed   []Failure `json:"failed"`
	Examined int       `json:"examined"`
}

// ManifestStore is the file manifest, which the migration updates in place
// rather than leaving to the next scan. *db.Queries satisfies it.
type ManifestStore interface {
	ListFileManifests(ctx context.Context) ([]db.FileManifest, error)
	UpsertFileManifest(ctx context.Context, arg db.UpsertFileManifestParams) error
	DeleteFileManifest(ctx context.Context, canonicalID string) error
}

// Service runs the migration and puts the library back in step with it.
type Service struct {
	musicDir string
	// manifest is carried across each rename so the content hash survives and
	// peers that already hold the bytes do not fetch them again. deviceID scopes
	// that to this device's own entries: a peer's row describes a file on that
	// peer's disk, under whatever name that peer gave it, and is not ours to
	// rewrite just because a path here happens to match.
	manifest ManifestStore
	deviceID string
	// reconcile brings the file manifest, the embedded library index and the
	// catalog's backend bindings back into agreement with what is now on disk.
	// It is supplied by the composition root because the migration should not
	// need to know how a library is indexed.
	reconcile func(context.Context) error
}

func New(musicDir string, reconcile func(context.Context) error) *Service {
	return &Service{musicDir: musicDir, reconcile: reconcile}
}

// WithManifest attaches the file manifest and the id of the device whose
// entries it may touch. Without it a run still renames correctly and the next
// library scan rebuilds the entries by re-hashing every moved file — right, but
// slow, and briefly leaving the moved tracks unadvertised.
func (s *Service) WithManifest(m ManifestStore, localDeviceID string) *Service {
	s.manifest, s.deviceID = m, localDeviceID
	return s
}

// mine reports whether a manifest entry describes a file on this device's disk.
func (s *Service) mine(deviceID string) bool {
	return s.deviceID == "" || deviceID == s.deviceID
}

// Pending counts this device's own files whose names another device in the
// household could not store.
//
// It reads the manifest rather than walking the disk: the manifest already
// records every file this device advertises, and the count exists to answer a
// question the UI asks on every visit — whether to offer the migration at all.
// The device that holds the unportable names is the one that has to run the
// migration, and it is not the device that notices the problem, so the offer
// cannot be driven by pull failures.
func (s *Service) Pending(ctx context.Context) (int, error) {
	if s == nil || s.manifest == nil {
		return 0, nil
	}
	rows, err := s.manifest.ListFileManifests(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		if s.mine(row.DeviceID) && !portablename.IsPortable(row.RelPath) {
			n++
		}
	}
	return n, nil
}

// Run renames every unportable file and directory under the music folder, then
// reconciles the library.
//
// Reconciliation runs even when some renames failed: the ones that succeeded
// have already moved on disk, and a library still pointing at their old paths
// would serve tracks that are not there.
func (s *Service) Run(ctx context.Context) (Result, error) {
	if s == nil || s.musicDir == "" {
		return Result{}, fmt.Errorf("portablemigrate: no music directory configured")
	}
	res, err := rename(ctx, s.musicDir)
	if err != nil {
		return res, err
	}
	if err := s.moveManifests(ctx, res.Renamed); err != nil {
		return res, fmt.Errorf("portablemigrate: renamed %d entries but could not update the file manifest: %w", len(res.Renamed), err)
	}
	// A run killed between the renames and the manifest update leaves the disk
	// migrated and the manifest pointing at names that are gone. Nothing in the
	// rename list survives that crash, so the repair cannot be driven from it —
	// it is derived from what is on disk instead, which is what makes rerunning
	// finish the job rather than find every name already portable and do
	// nothing.
	if err := s.repairStragglers(ctx); err != nil {
		return res, fmt.Errorf("portablemigrate: could not reconcile the file manifest with what is on disk: %w", err)
	}
	if s.reconcile != nil {
		if rerr := s.reconcile(ctx); rerr != nil {
			return res, fmt.Errorf("portablemigrate: renamed %d entries but could not reconcile the library: %w", len(res.Renamed), rerr)
		}
	}
	return res, nil
}

// rename does the filesystem half. It is separate from Run so the ordering and
// collision rules can be tested without a library behind them.
func rename(ctx context.Context, musicDir string) (Result, error) {
	var res Result
	type entry struct {
		rel   string
		depth int
		isDir bool
	}
	var entries []entry
	walkErr := filepath.WalkDir(musicDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is not a reason to rename nothing; note it
			// and carry on with what can be read.
			res.Failed = append(res.Failed, Failure{Path: path, Reason: err.Error()})
			return nil
		}
		if path == musicDir {
			return nil
		}
		// Hidden entries are skipped for the same reason the library scan skips
		// them: they are not the owner's music, and a leading dot is legal
		// everywhere anyway.
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(musicDir, path)
		if relErr != nil {
			return nil
		}
		slash := filepath.ToSlash(rel)
		entries = append(entries, entry{rel: slash, depth: strings.Count(slash, "/"), isDir: d.IsDir()})
		return nil
	})
	if walkErr != nil {
		return res, walkErr
	}
	res.Examined = len(entries)
	// Deepest first. Renaming a directory changes the path of everything
	// beneath it, so its contents have to be moved while its own name is still
	// the one they were found under.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].depth > entries[j].depth })

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			// A cancelled migration is a partial one, which is a state this is
			// built to be left in. Report what was done.
			return res, nil
		}
		dir, name := filepath.Split(filepath.FromSlash(e.rel))
		want := portablename.Segment(name)
		if want == name {
			continue
		}
		parent := filepath.Join(musicDir, dir)
		// A move that linked the new name but died before unlinking the old one
		// leaves the same bytes under both. Minting "Name (2)" for the leftover
		// would turn that duplicate into a third copy under a name the owner
		// never chose; removing it finishes the move the previous run started.
		// Same-file is an identity check, not a content comparison: these are
		// one inode reached by two names, which is exactly what the link left.
		if done, err := finishInterruptedMove(parent, name, want); err != nil {
			res.Failed = append(res.Failed, Failure{Path: e.rel, Reason: err.Error()})
			continue
		} else if done {
			res.Renamed = append(res.Renamed, Rename{
				From: e.rel,
				To:   filepath.ToSlash(filepath.Join(dir, want)),
			})
			continue
		}
		free, err := portablename.Unique(parent, want)
		if err != nil {
			res.Failed = append(res.Failed, Failure{Path: e.rel, Reason: err.Error()})
			continue
		}
		if err := portablename.MoveWithoutClobbering(parent, name, free, e.isDir); err != nil {
			res.Failed = append(res.Failed, Failure{Path: e.rel, Reason: err.Error()})
			continue
		}
		res.Renamed = append(res.Renamed, Rename{
			From: e.rel,
			To:   filepath.ToSlash(filepath.Join(dir, free)),
		})
	}
	return res, nil
}

// moveManifests carries each renamed file's manifest entry to its new path.
//
// The content hash is what matters here. A peer decides what to fetch by
// comparing hashes, not paths, so an entry that arrives at its new path with
// the same hash costs no re-transfer of bytes the peer already holds — whereas
// deleting the row and letting the next scan rebuild it would re-hash every
// migrated file to reach the same answer.
//
// The canonical id necessarily changes, because it is "<deviceId>:<relPath>"
// and the path is half of it. That is an addressing change, not an identity
// change in the sense that costs anything: size, mtime and hash are carried
// over unchanged, and the old row is removed in the same pass so the two never
// both advertise the file.
func (s *Service) moveManifests(ctx context.Context, renamed []Rename) error {
	if s.manifest == nil || len(renamed) == 0 {
		return nil
	}
	rows, err := s.manifest.ListFileManifests(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !s.mine(row.DeviceID) {
			continue
		}
		// The renames are replayed in the order they were applied, because a
		// renamed directory moves every path beneath it and its contents were
		// moved first. Replaying them reproduces the path the file now has.
		newPath := row.RelPath
		for _, mv := range renamed {
			if rest, ok := under(newPath, mv.From); ok {
				newPath = mv.To + rest
			}
		}
		if newPath == row.RelPath {
			continue
		}
		if err := s.movePath(ctx, row, newPath); err != nil {
			return err
		}
	}
	return nil
}

// movePath re-addresses one manifest entry, carrying everything that describes
// the bytes — hash, size, mtime — across unchanged, so a peer that already
// holds them has no reason to ask for them again. The canonical id is
// "<deviceId>:<relPath>", so moving the path necessarily mints a new one; the
// old row is removed in the same breath, or the file would be advertised twice,
// once at a path that is gone.
func (s *Service) movePath(ctx context.Context, row db.FileManifest, newPath string) error {
	if err := s.manifest.UpsertFileManifest(ctx, db.UpsertFileManifestParams{
		CanonicalID: row.DeviceID + ":" + newPath,
		ContentHash: row.ContentHash,
		Size:        row.Size,
		RelPath:     newPath,
		Mtime:       row.Mtime,
		DeviceID:    row.DeviceID,
	}); err != nil {
		return err
	}
	return s.manifest.DeleteFileManifest(ctx, row.CanonicalID)
}

// under reports whether path is prefix itself or something beneath it,
// returning the remainder. Comparing on a component boundary keeps "Etc" from
// matching "Etcetera".
func under(path, prefix string) (string, bool) {
	if path == prefix {
		return "", true
	}
	if strings.HasPrefix(path, prefix+"/") {
		return path[len(prefix):], true
	}
	return "", false
}

// repairStragglers moves manifest entries whose file is no longer where they
// say it is, but is at the portable name this migration would have given it.
//
// The mapping is deterministic, so the repair needs no record of what a
// previous run did. An entry whose file went somewhere else — a collision
// resolved to "… (2)", or a file the owner moved themselves — is left to the
// library scan, which finds it by re-hashing; guessing here would be how a
// manifest comes to point at the wrong bytes.
func (s *Service) repairStragglers(ctx context.Context) error {
	if s.manifest == nil {
		return nil
	}
	rows, err := s.manifest.ListFileManifests(ctx)
	if err != nil {
		return err
	}
	claimed := make(map[string]bool, len(rows))
	for _, r := range rows {
		claimed[r.RelPath] = true
	}
	for _, row := range rows {
		if !s.mine(row.DeviceID) {
			continue
		}
		want := portablename.RelPath(row.RelPath)
		if want == row.RelPath || claimed[want] {
			continue
		}
		if exists(s.musicDir, row.RelPath) || !exists(s.musicDir, want) {
			continue
		}
		if err := s.movePath(ctx, row, want); err != nil {
			return err
		}
		claimed[want] = true
	}
	return nil
}

func exists(musicDir, rel string) bool {
	_, err := os.Lstat(filepath.Join(musicDir, filepath.FromSlash(rel)))
	return err == nil
}

// finishInterruptedMove completes a move that a previous run left half done,
// reporting whether it did. A leftover is recognised by being the very same
// file as the name it was being moved to — one inode under two names, which is
// what linking the new name and not yet unlinking the old one leaves behind.
func finishInterruptedMove(parent, from, to string) (bool, error) {
	oldInfo, err := os.Lstat(filepath.Join(parent, from))
	if err != nil {
		return false, nil
	}
	newInfo, err := os.Lstat(filepath.Join(parent, to))
	if err != nil || !os.SameFile(oldInfo, newInfo) {
		return false, nil
	}
	if err := os.Remove(filepath.Join(parent, from)); err != nil {
		return false, fmt.Errorf("portablemigrate: %q is already linked to %q but could not be removed: %w", from, to, err)
	}
	return true, nil
}
