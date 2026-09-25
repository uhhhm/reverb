package offlineset

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/audiotag"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/p2p"
	"github.com/uhhhm/reverb/internal/store/db"
)

// DefaultReserve is the free space a phone keeps for everything else on it.
// Fetching stops before the disk goes below it.
const DefaultReserve = 1 << 30

// TrackState is where one offline track's file is.
type TrackState string

const (
	// StateReady: the file is on this device.
	StateReady TrackState = "ready"
	// StateFetching: the file is being copied from a paired device.
	StateFetching TrackState = "fetching"
	// StateQueued: a paired device offers the file and it will be fetched.
	StateQueued TrackState = "queued"
	// StateNoSpace: a paired device offers the file, but it does not fit.
	StateNoSpace TrackState = "noSpace"
	// StateUnavailable: no paired device reached since startup offers it.
	StateUnavailable TrackState = "unavailable"
)

// KeeperStore is what the keeper reads and records. *db.Queries satisfies it.
type KeeperStore interface {
	ListOfflineSetForDevice(ctx context.Context, deviceID string) ([]db.OfflineSet, error)
	GetSyncedPlaylist(ctx context.Context, id string) (db.SyncedPlaylist, error)
	ListFileManifests(ctx context.Context) ([]db.FileManifest, error)
	ListFileTags(ctx context.Context) ([]db.FileTag, error)
	UpsertOfflineFile(ctx context.Context, arg db.UpsertOfflineFileParams) error
	ListOfflineFiles(ctx context.Context) ([]db.OfflineFile, error)
	DeleteOfflineFile(ctx context.Context, relPath string) error
	UpsertPendingUpload(ctx context.Context, arg db.UpsertPendingUploadParams) error
	ListPendingUploads(ctx context.Context) ([]db.PendingUpload, error)
	DeletePendingUpload(ctx context.Context, relPath string) error
}

// KeeperConfig wires a Keeper.
type KeeperConfig struct {
	Store KeeperStore
	// DeviceID is the device the offline set rows are recorded under.
	DeviceID func(ctx context.Context) (string, error)
	// FileDeviceID is the device id this device's own file manifest rows carry.
	FileDeviceID string
	MusicDir     string
	// FreeSpace reports the bytes free on the disk holding dir. Nil asks the
	// operating system.
	FreeSpace func(dir string) (int64, error)
	// Reserve is the free space fetching leaves alone; zero is DefaultReserve.
	Reserve int64
	// Rescan brings the file manifest and the library up to date after files
	// are pruned.
	Rescan func(ctx context.Context) error
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Keeper keeps a phone's offline set on the phone (ADR 0003). A desktop
// replicates every file; a phone holds only the tracks of the playlists it
// marked offline, and the Downloads made on it until a paired device holds
// them (see AddPending). The keeper narrows each peer's file manifest to those
// tracks, records what it fetched, prunes what no offline playlist names any
// more, and reports storage and per-track progress.
//
// A playlist names a track by title, artist and album; a manifest names a file
// by path and hash plus what its tags say. The two meet through the matcher's
// own decision (matching.Resolve), so a file the keeper fetches for a track is
// the file the phone's library then resolves that track to.
//
// When the files wanted do not fit, fetching stops at the first that does not
// and the status says so; nothing already on the phone is removed to make
// room. Only a file the keeper fetched is ever pruned, and only once no
// offline playlist names it.
type Keeper struct {
	cfg KeeperConfig

	mu sync.Mutex
	// offered is each peer's latest manifest, audio files only. A status has
	// nothing else to tell "no device holds this" from "not asked yet".
	offered map[string][]p2p.FileManifest
	// fetching is every file a round selected and has not finished with,
	// keyed on content. Rounds for different peers run at once; claiming a
	// file when it is selected is what keeps them from choosing it twice or
	// together spending more space than there is.
	fetching map[string]*fetchProgress
	kick     func()
}

type fetchProgress struct {
	done, total int64
	started     bool
}

// NewKeeper builds a keeper.
func NewKeeper(cfg KeeperConfig) *Keeper {
	if cfg.FreeSpace == nil {
		cfg.FreeSpace = FreeSpace
	}
	if cfg.Reserve <= 0 {
		cfg.Reserve = DefaultReserve
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Keeper{cfg: cfg, offered: map[string][]p2p.FileManifest{}, fetching: map[string]*fetchProgress{}}
}

// SetKick installs what Changed calls to start a fetch round.
func (k *Keeper) SetKick(fn func()) {
	k.mu.Lock()
	k.kick = fn
	k.mu.Unlock()
}

// Changed reports that the offline set or a playlist in it changed, so the
// files it needs are fetched and pruned now rather than at the next round.
func (k *Keeper) Changed() {
	k.mu.Lock()
	kick := k.kick
	k.mu.Unlock()
	if kick != nil {
		kick()
	}
}

// member is one track of an enabled offline playlist and where its file is.
type member struct {
	track core.ExternalResult
	local *file
	// offer is the file a peer offers for it, when it is not local.
	offer *p2p.FileManifest
}

type file struct {
	rel, hash string
	size      int64
}

type playlistPlan struct {
	id, name string
	members  []*member
}

// candidates indexes files the way the matcher narrows them: its fuzzy rung
// needs equal normalised titles, its ISRC rung equal ISRCs.
type candidates struct {
	byTitle map[string][]core.Track
	byISRC  map[string][]core.Track
}

func newCandidates() *candidates {
	return &candidates{byTitle: map[string][]core.Track{}, byISRC: map[string][]core.Track{}}
}

func (c *candidates) add(key, title, artist, album, isrc string) {
	t := core.Track{ID: key, Title: title, Artist: artist, Album: album, ISRC: isrc}
	n := matching.Normalize(title)
	c.byTitle[n] = append(c.byTitle[n], t)
	if isrc != "" {
		c.byISRC[isrc] = append(c.byISRC[isrc], t)
	}
}

// match returns the key of the file the matcher would choose for t.
func (c *candidates) match(t core.ExternalResult) (string, bool) {
	cands := c.byTitle[matching.Normalize(t.Title)]
	if t.ISRC != "" {
		cands = append(append([]core.Track{}, c.byISRC[t.ISRC]...), cands...)
	}
	if len(cands) == 0 {
		return "", false
	}
	res := matching.Resolve(t, cands)
	if res.Status != core.MatchInLibrary {
		return "", false
	}
	return res.LibraryTrackID, true
}

// plan reads the enabled offline playlists and finds each track's file on this
// device. offers, when given, fills in what a peer offers for the rest.
func (k *Keeper) plan(ctx context.Context, offers []p2p.FileManifest) ([]playlistPlan, error) {
	deviceID, err := k.cfg.DeviceID(ctx)
	if err != nil {
		return nil, err
	}
	entries, err := k.cfg.Store.ListOfflineSetForDevice(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	var plans []playlistPlan
	for _, e := range entries {
		if e.Enabled == 0 {
			continue
		}
		row, err := k.cfg.Store.GetSyncedPlaylist(ctx, e.PlaylistID)
		if errors.Is(err, sql.ErrNoRows) {
			// A playlist deleted since it was marked offline names nothing.
			continue
		}
		if err != nil {
			return nil, err
		}
		var tracks []core.ExternalResult
		if err := json.Unmarshal([]byte(row.TracksJson), &tracks); err != nil && row.TracksJson != "" {
			return nil, err
		}
		p := playlistPlan{id: row.ID, name: row.Name}
		for _, t := range tracks {
			p.members = append(p.members, &member{track: t})
		}
		plans = append(plans, p)
	}
	if len(plans) == 0 {
		return nil, nil
	}

	locals, err := k.localFiles(ctx)
	if err != nil {
		return nil, err
	}
	local := newCandidates()
	for key, f := range locals {
		local.add(key, f.title, f.artist, f.album, f.isrc)
	}
	offered := newCandidates()
	offerByHash := make(map[string]*p2p.FileManifest, len(offers))
	for i := range offers {
		o := &offers[i]
		if o.Title == "" || !audiotag.IsAudio(o.RelPath) {
			continue
		}
		if _, dup := offerByHash[o.ContentHash]; dup {
			continue
		}
		offerByHash[o.ContentHash] = o
		offered.add(o.ContentHash, o.Title, o.Artist, o.Album, o.ISRC)
	}
	for _, p := range plans {
		for _, m := range p.members {
			if key, ok := local.match(m.track); ok {
				f := locals[key]
				m.local = &f.file
				continue
			}
			if hash, ok := offered.match(m.track); ok {
				m.offer = offerByHash[hash]
			}
		}
	}
	return plans, nil
}

type localFile struct {
	file
	title, artist, album, isrc string
}

// localManifestFiles is every file this device's manifest lists, keyed on its
// path, whether or not it has readable audio tags yet.
func (k *Keeper) localManifestFiles(ctx context.Context) (map[string]file, error) {
	rows, err := k.cfg.Store.ListFileManifests(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]file{}
	for _, r := range rows {
		if r.DeviceID == k.cfg.FileDeviceID {
			out[r.RelPath] = file{rel: r.RelPath, hash: r.ContentHash, size: r.Size}
		}
	}
	return out, nil
}

// localFiles is this device's own tagged audio files, keyed on their path.
func (k *Keeper) localFiles(ctx context.Context) (map[string]localFile, error) {
	files, err := k.localManifestFiles(ctx)
	if err != nil {
		return nil, err
	}
	return k.taggedLocalFiles(ctx, files)
}

func (k *Keeper) taggedLocalFiles(ctx context.Context, files map[string]file) (map[string]localFile, error) {
	tagRows, err := k.cfg.Store.ListFileTags(ctx)
	if err != nil {
		return nil, err
	}
	tags := make(map[string]db.FileTag, len(tagRows))
	for _, t := range tagRows {
		tags[t.ContentHash] = t
	}
	out := map[string]localFile{}
	for rel, f := range files {
		if !audiotag.IsAudio(rel) {
			continue
		}
		t, ok := tags[f.hash]
		if !ok {
			continue
		}
		out[rel] = localFile{
			file:  f,
			title: t.Title, artist: t.Artist, album: t.Album, isrc: t.Isrc,
		}
	}
	return out, nil
}

// free is the disk's free space less the reserve, and whether the disk said.
// A disk that cannot say gets nothing fetched, since the reserve could not be
// kept, but it is not reported as full either: nothing says it is.
func (k *Keeper) free() (int64, bool) {
	free, err := k.cfg.FreeSpace(k.cfg.MusicDir)
	if err != nil {
		log.Printf("offline set: free space of %s: %v", k.cfg.MusicDir, err)
		return 0, false
	}
	if free -= k.cfg.Reserve; free < 0 {
		return 0, true
	}
	return free, true
}

// budgetLocked is what fetching may still use: free less what claimed files
// have still to write. k.mu is held.
func (k *Keeper) budgetLocked(free int64) int64 {
	for _, p := range k.fetching {
		if p.total > p.done {
			free -= p.total - p.done
		}
	}
	if free < 0 {
		return 0
	}
	return free
}

// Select narrows the files missing from a peer to the ones the offline set
// wants, in playlist order, stopping at the first that does not fit. offered
// is the peer's whole manifest, which a status needs to say what the peer
// holds even when it is not missing.
func (k *Keeper) Select(ctx context.Context, peerID string, offered, missing []p2p.FileManifest) []p2p.FileManifest {
	audio := make([]p2p.FileManifest, 0, len(offered))
	for _, o := range offered {
		if audiotag.IsAudio(o.RelPath) {
			audio = append(audio, o)
		}
	}
	k.mu.Lock()
	k.offered[peerID] = audio
	k.mu.Unlock()

	plans, err := k.plan(ctx, audio)
	if err != nil || len(plans) == 0 {
		return nil
	}
	isMissing := make(map[string]p2p.FileManifest, len(missing))
	for _, m := range missing {
		isMissing[m.ContentHash] = m
	}
	free, _ := k.free()
	var out []p2p.FileManifest
	seen := map[string]bool{}
	k.mu.Lock()
	budget := k.budgetLocked(free)
	for _, p := range plans {
		for _, m := range p.members {
			if m.local != nil || m.offer == nil || seen[m.offer.ContentHash] {
				continue
			}
			seen[m.offer.ContentHash] = true
			f, ok := isMissing[m.offer.ContentHash]
			if _, claimed := k.fetching[m.offer.ContentHash]; !ok || claimed {
				continue
			}
			if f.Size > budget {
				k.mu.Unlock()
				return k.claimed(ctx, out)
			}
			budget -= f.Size
			k.fetching[f.ContentHash] = &fetchProgress{total: f.Size}
			out = append(out, f)
		}
	}
	k.mu.Unlock()
	return k.claimed(ctx, out)
}

// claimed records the selected files as the offline set's before any byte
// arrives, so a file that lands and is never reported, because the app was
// killed mid-round, is still pruned later. Prune forgets the record of one
// that never arrived.
func (k *Keeper) claimed(ctx context.Context, files []p2p.FileManifest) []p2p.FileManifest {
	for _, f := range files {
		_ = k.cfg.Store.UpsertOfflineFile(ctx, db.UpsertOfflineFileParams{
			RelPath: f.RelPath, ContentHash: f.ContentHash, FetchedAt: k.cfg.Now().UnixMilli(),
		})
	}
	return files
}

// Progress records bytes copied so far for a fetch of the given content. A
// fetch the keeper did not select, such as a manual one, is not tracked.
func (k *Keeper) Progress(hash string, done int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if p, ok := k.fetching[hash]; ok {
		p.done, p.started = done, true
	}
}

// Fetched releases a selected file once its fetch is over, or once the round
// passed it by. Its record stays: Prune drops that if the file never came.
func (k *Keeper) Fetched(_ context.Context, f p2p.FileManifest, _ error) {
	k.mu.Lock()
	delete(k.fetching, f.ContentHash)
	k.mu.Unlock()
}

// Prune removes the files the keeper fetched that no offline playlist names
// any more, and the Downloads made here that a paired device now holds and no
// offline playlist names. A Download no peer has confirmed is never removed.
// Anything else in the folder is left alone.
func (k *Keeper) Prune(ctx context.Context) error {
	owned, err := k.cfg.Store.ListOfflineFiles(ctx)
	if err != nil {
		return err
	}
	pending, err := k.cfg.Store.ListPendingUploads(ctx)
	if err != nil || (len(owned) == 0 && len(pending) == 0) {
		return err
	}
	plans, err := k.plan(ctx, nil)
	if err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, p := range plans {
		for _, m := range p.members {
			if m.local != nil {
				keep[m.local.rel] = true
			}
		}
	}
	locals, err := k.localFiles(ctx)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(k.cfg.MusicDir)
	if err != nil {
		return err
	}
	defer root.Close()
	protect, removed, err := k.settlePending(ctx, root, keep)
	if err != nil {
		return err
	}
	for _, o := range owned {
		if keep[o.RelPath] || protect[o.RelPath] {
			continue
		}
		// Only the bytes that were fetched are removed. A file replaced at
		// that path since is no longer the offline set's: it is forgotten and
		// left where it is. One on disk but not scanned yet waits for the scan.
		cur, scanned := locals[o.RelPath]
		switch {
		case scanned && cur.hash == o.ContentHash:
			if err := root.Remove(filepath.FromSlash(o.RelPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				continue
			}
			removed = true
			removeEmptyParents(root, o.RelPath)
		case !scanned:
			if _, err := root.Stat(filepath.FromSlash(o.RelPath)); err == nil {
				continue
			}
		}
		_ = k.cfg.Store.DeleteOfflineFile(ctx, o.RelPath)
	}
	if removed && k.cfg.Rescan != nil {
		return k.cfg.Rescan(ctx)
	}
	return nil
}

func dbPending(rel string, at int64) db.UpsertPendingUploadParams {
	return db.UpsertPendingUploadParams{RelPath: rel, DownloadedAt: at}
}

func dbOffline(rel, hash string, at int64) db.UpsertOfflineFileParams {
	return db.UpsertOfflineFileParams{RelPath: rel, ContentHash: hash, FetchedAt: at}
}

// Status is the offline set's storage and progress.
type Status struct {
	// UsedBytes is the space the offline set's files take, each counted once
	// however many playlists name it.
	UsedBytes int64 `json:"usedBytes"`
	// AvailableBytes is what fetching may still use: free space less the
	// reserve kept for the rest of the phone.
	AvailableBytes int64 `json:"availableBytes"`
	// Full is set when a track a paired device offers does not fit. Fetching
	// has stopped, and nothing was removed to make room.
	Full bool `json:"full"`
	// SpaceUnknown is set when the disk's free space cannot be read; fetching
	// waits until it can.
	SpaceUnknown bool             `json:"spaceUnknown"`
	Playlists    []PlaylistStatus `json:"playlists"`
}

// PlaylistStatus is one offline playlist.
type PlaylistStatus struct {
	PlaylistID   string        `json:"playlistId"`
	PlaylistName string        `json:"playlistName"`
	Bytes        int64         `json:"bytes"`
	TrackCount   int           `json:"trackCount"`
	ReadyCount   int           `json:"readyCount"`
	Tracks       []TrackStatus `json:"tracks"`
}

// TrackStatus is one track of an offline playlist.
type TrackStatus struct {
	Title  string     `json:"title"`
	Artist string     `json:"artist"`
	Album  string     `json:"album,omitempty"`
	State  TrackState `json:"state"`
	// SizeBytes is the file's size, once a file is known; FetchedBytes is how
	// much of it is on this device.
	SizeBytes    int64 `json:"sizeBytes"`
	FetchedBytes int64 `json:"fetchedBytes"`
}

// Status reports the offline set against what paired devices offered when
// last asked.
func (k *Keeper) Status(ctx context.Context) (Status, error) {
	k.mu.Lock()
	var offers []p2p.FileManifest
	for _, files := range k.offered {
		offers = append(offers, files...)
	}
	fetching := make(map[string]fetchProgress, len(k.fetching))
	for h, p := range k.fetching {
		fetching[h] = *p
	}
	k.mu.Unlock()

	plans, err := k.plan(ctx, offers)
	if err != nil {
		return Status{}, err
	}
	free, known := k.free()
	k.mu.Lock()
	st := Status{AvailableBytes: k.budgetLocked(free), SpaceUnknown: !known, Playlists: []PlaylistStatus{}}
	k.mu.Unlock()
	budget := st.AvailableBytes
	counted := map[string]bool{}
	planned := map[string]bool{}
	for _, p := range plans {
		ps := PlaylistStatus{PlaylistID: p.id, PlaylistName: p.name, TrackCount: len(p.members), Tracks: []TrackStatus{}}
		for _, m := range p.members {
			ts := TrackStatus{Title: m.track.Title, Artist: m.track.Artist, Album: m.track.Album, State: StateUnavailable}
			switch {
			case m.local != nil:
				ts.State, ts.SizeBytes, ts.FetchedBytes = StateReady, m.local.size, m.local.size
				ps.ReadyCount++
				ps.Bytes += m.local.size
				if !counted[m.local.rel] {
					counted[m.local.rel] = true
					st.UsedBytes += m.local.size
				}
			case m.offer != nil:
				ts.SizeBytes = m.offer.Size
				if prog, ok := fetching[m.offer.ContentHash]; ok {
					// Selected by a round: its space is already set aside.
					ts.State, ts.FetchedBytes = StateQueued, prog.done
					if prog.started {
						ts.State = StateFetching
					}
					break
				}
				ts.State = StateQueued
				if !planned[m.offer.ContentHash] {
					planned[m.offer.ContentHash] = true
					// With the free space unknown nothing is fetched, but
					// nothing says it will not fit either.
					switch {
					case st.SpaceUnknown:
					case st.Full || m.offer.Size > budget:
						st.Full = true
					default:
						budget -= m.offer.Size
					}
				}
				if st.Full {
					ts.State = StateNoSpace
				}
			}
			ps.Tracks = append(ps.Tracks, ts)
		}
		st.Playlists = append(st.Playlists, ps)
	}
	return st, nil
}
