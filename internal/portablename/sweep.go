package portablename

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Renamed records one file a sweep moved, for the caller's log.
type Renamed struct {
	From string
	To   string
}

// Snapshot records the names directly inside dir. A download adapter takes one
// before it runs so the sweep afterwards can tell the file this download
// produced from the rest of the library, without depending on mtime — yt-dlp
// stamps a downloaded file with the source's upload date, so "modified since I
// started" is not a reliable test.
//
// A dir that does not exist yet is an empty snapshot, not an error: the adapter
// creates its output directory on first use.
func Snapshot(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out, nil
}

// SweepNew renames the files that appeared in dir since the snapshot onto
// portable names, and reports what it moved.
//
// This is the half of portable naming that an output template cannot cover. An
// adapter that substitutes its own placeholders — spotDL's {artists} and {title},
// yt-dlp's %(title)s fallback — chooses the name itself from source metadata,
// so the only place left to enforce portability is the name on disk once it
// exists. Renaming before the library scan sees the file keeps the unportable
// name from ever reaching the catalog or a peer's manifest.
//
// Only the top level of dir is swept, because both adapters are given a flat
// "<dir>/<artist> - <title>.<ext>" template and neither creates subdirectories.
// Files already present when the snapshot was taken are left alone: making an
// established library portable is a migration, not a side effect of one
// download.
//
// Downloads run concurrently into one shared output directory, so "new since
// the snapshot" can also mean "another job's file". A finished one is harmless
// to rename — it needs the same treatment and the library scan finds it either
// way — but a partial one must not be touched, or the job still writing it
// fails. Those are recognisable by extension and skipped.
func SweepNew(dir string, before map[string]bool) ([]Renamed, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var moved []Renamed
	var firstErr error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || before[name] || inProgress(name) {
			continue
		}
		want := Segment(name)
		if want == name {
			continue
		}
		free, err := Unique(dir, want)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := MoveWithoutClobbering(dir, name, free, false); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("portablename: rename %q to %q: %w", name, free, err)
			}
			continue
		}
		moved = append(moved, Renamed{From: name, To: free})
	}
	return moved, firstErr
}

// MoveWithoutClobbering renames an entry within dir, never replacing whatever
// may be at the destination.
//
// Unique picks a free name, but between that check and the move something else
// can take it — a download landing while a migration runs. For a file the move
// goes through a hard link, which the filesystem refuses when the destination
// exists, closing that window; the worst an interruption can leave is the same
// bytes under both names, which is a duplicate rather than a loss.
//
// Directories cannot be hard-linked, and neither can a file on a filesystem
// without link support (an exFAT external drive). Both fall back to a plain
// rename, which still refuses a destination occupied by a non-empty directory
// and is what this did before. Losing the guarantee is better than refusing to
// migrate the library at all.
func MoveWithoutClobbering(dir, from, to string, isDir bool) error {
	oldPath, newPath := filepath.Join(dir, from), filepath.Join(dir, to)
	if !isDir {
		if err := os.Link(oldPath, newPath); err == nil {
			if err := os.Remove(oldPath); err != nil {
				// The link is in place, so the entry is reachable under its new
				// name; leaving the old one costs a duplicate, not the file.
				return fmt.Errorf("portablename: linked %q to %q but could not remove the original: %w", from, to, err)
			}
			return nil
		} else if !errors.Is(err, fs.ErrExist) && !linkUnsupported(err) {
			return err
		} else if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("portablename: %q is already taken", to)
		}
	}
	return os.Rename(oldPath, newPath)
}

// linkUnsupported reports whether the filesystem cannot hard-link at all, as
// opposed to refusing this particular link.
func linkUnsupported(err error) bool {
	return errors.Is(err, errors.ErrUnsupported) || errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrInvalid)
}

// inProgressExts are the suffixes a download tool gives a file it has not
// finished with: yt-dlp's ".part" and ".ytdl", and the ".temp"/".tmp" a
// post-processor writes beside its target before renaming over it.
var inProgressExts = map[string]bool{
	".part":     true,
	".ytdl":     true,
	".temp":     true,
	".tmp":      true,
	".download": true,
}

// inProgress reports whether a name belongs to a download still being written.
// A leading dot counts too: that is how both tools and this package's own fetch
// path mark scratch files.
func inProgress(name string) bool {
	return strings.HasPrefix(name, ".") || inProgressExts[strings.ToLower(filepath.Ext(name))]
}
