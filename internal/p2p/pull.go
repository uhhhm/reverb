package p2p

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/portablename"
	"github.com/uhhhm/reverb/internal/store/db"
)

// Puller replicates files from paired peers. Every round it asks each trusted
// peer for its manifest, diffs it against what this device holds, and fetches
// what is missing. This is the half of file sync that makes a paired device's
// library actually appear locally; the manual /p2p/fetch endpoint stays as an
// escape hatch.
type Puller struct {
	host        host.Host
	store       FileStore
	files       *FileSyncer
	guard       *Guard
	deviceID    string
	musicDir    string
	interval    time.Duration
	maxPerRound int

	// covers and coverDir are the artwork half of a pull. The sync log carries
	// only a cover's content hash, so a device that accepted a peer's cover
	// change has a row pointing at bytes it does not hold until this fetches
	// them. Both nil/empty leaves cover pulling off.
	covers   CoverRefLister
	coverDir string

	// failures is how a file that can never be fetched stops crowding out the
	// ones that can. Optional: a store without the capability degrades to the
	// old behaviour of retrying everything every round.
	failures *fetchFailures

	// selection narrows what is fetched. Nil fetches everything, which is
	// what a desktop does; a phone keeps only its offline set.
	selection FileSelection

	// kick asks Run for a round now; round serialises rounds, since one can
	// come from Run and one from PullNow at the same time.
	kick  chan struct{}
	round sync.Mutex
}

// errNotAttempted is the outcome reported for a selected file the round did
// not get to.
var errNotAttempted = errors.New("not attempted this round")

// FileSelection decides which of a peer's files this device keeps.
type FileSelection interface {
	// Select narrows missing, the files peerID offers that this device lacks,
	// to the ones it wants, in the order to fetch them. offered is the peer's
	// whole manifest.
	Select(ctx context.Context, peerID string, offered, missing []FileManifest) []FileManifest
	// Fetched reports that the round is done with a selected file: fetched
	// when err is nil, failed, or not attempted.
	Fetched(ctx context.Context, f FileManifest, err error)
	// Prune runs after every round to drop what is no longer wanted.
	Prune(ctx context.Context) error
}

// WithSelection makes the puller fetch only what sel selects.
func (p *Puller) WithSelection(sel FileSelection) *Puller {
	p.selection = sel
	return p
}

// Kick asks for a round as soon as the current one, if any, is done. It
// never blocks; kicks while one is pending collapse into it.
func (p *Puller) Kick() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// PullNow runs one round and returns when it is done.
func (p *Puller) PullNow(ctx context.Context) {
	if p == nil || p.host == nil || p.store == nil || p.files == nil || p.guard == nil {
		return
	}
	p.pullAll(ctx)
}

// CoverRefLister reads the covers this device knows about, whether or not it
// holds their bytes. *db.Queries satisfies it.
type CoverRefLister interface {
	ListEntityCovers(ctx context.Context) ([]db.EntityCover, error)
}

// WithCovers attaches artwork pulling.
func (p *Puller) WithCovers(c CoverRefLister, coverDir string) *Puller {
	p.covers, p.coverDir = c, coverDir
	return p
}

func NewPuller(h host.Host, store FileStore, files *FileSyncer, guard *Guard, localDeviceID, musicDir string) *Puller {
	p := &Puller{
		host:     h,
		store:    store,
		files:    files,
		guard:    guard,
		deviceID: localDeviceID,
		musicDir: musicDir,
		interval: 2 * time.Minute,
		// Bounds one round's work so a large peer library is pulled over
		// several rounds instead of saturating the link in one go.
		maxPerRound: 50,
		failures:    &fetchFailures{},
		kick:        make(chan struct{}, 1),
	}
	if q, ok := store.(FetchFailureStore); ok {
		p.failures.q = q
	}
	return p
}

// Run pulls on an interval until ctx is canceled.
func (p *Puller) Run(ctx context.Context) {
	if p == nil || p.host == nil || p.store == nil || p.files == nil || p.guard == nil {
		<-ctx.Done()
		return
	}
	t := time.NewTicker(p.interval)
	defer t.Stop()
	timer := time.NewTimer(20 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return
	case <-timer.C:
		p.pullAll(ctx)
	case <-p.kick:
		timer.Stop()
		p.pullAll(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pullAll(ctx)
		case <-p.kick:
			p.pullAll(ctx)
		}
	}
}

func (p *Puller) pullAll(ctx context.Context) {
	p.round.Lock()
	defer p.round.Unlock()
	if p.selection != nil {
		// Pruning needs no peer: a playlist unmarked while every device is out
		// of reach still gives its space back.
		defer func() {
			if err := p.selection.Prune(ctx); err != nil {
				log.Printf("p2p pull: prune: %v", err)
			}
		}()
	}
	trusted, err := p.guard.TrustedPeers(ctx)
	if err != nil {
		log.Printf("p2p pull: trusted peer lookup failed: %v", err)
		return
	}
	var wg sync.WaitGroup
	for pid := range trusted {
		if pid == p.host.ID() {
			continue
		}
		pid := pid
		wg.Add(1)
		go func() {
			defer wg.Done()
			SafeRun("pull peer", func() {
				if !EnsureConnected(ctx, p.host, p.guard, pid) {
					p.offersNothing(ctx, pid)
					return
				}
				if err := p.pullPeer(ctx, pid); err != nil {
					log.Printf("p2p pull: peer %s: %v", pid, err)
				}
			})
		}()
	}
	wg.Wait()
}

// offersNothing tells the selection that pid, unreachable or empty, offers no
// file this round, so nothing is reported as on its way from it.
func (p *Puller) offersNothing(ctx context.Context, pid peer.ID) {
	if p.selection != nil {
		p.selection.Select(ctx, pid.String(), nil, nil)
	}
}

func (p *Puller) pullPeer(ctx context.Context, pid peer.ID) error {
	resp, err := RequestManifest(ctx, p.host, pid)
	if err != nil {
		p.offersNothing(ctx, pid)
		return err
	}
	// A round that got this far proves the current address works; persist it so
	// the next restart can dial the peer without discovery.
	if err := p.guard.RememberAddrs(ctx, pid, ObservedAddrs(p.host, pid)); err != nil {
		log.Printf("p2p pull: remember addrs for %s: %v", pid, err)
	}
	// Covers are pulled on every round, whatever the file lane does. Two
	// libraries in steady state have no missing files, and a cover uploaded on
	// one device would otherwise never be fetched: its entity_cover row arrives
	// through the sync log, but the bytes only ever followed a file transfer.
	defer func() {
		p.pullCovers(ctx, pid)
		p.guard.Touch(ctx, pid)
	}()
	if len(resp.Files) == 0 || p.musicDir == "" {
		// A peer offering nothing cannot be the reason anything is stuck.
		p.failures.prune(ctx, pid.String(), nil)
		p.offersNothing(ctx, pid)
		return nil
	}
	local, err := p.localManifests(ctx)
	if err != nil {
		return err
	}
	want := MissingFiles(local, resp.Files, p.musicDir)
	// Every file the selection chose is handed back when the round is done
	// with it, fetched or passed by (backed off, over the round's cap, a
	// cancelled round), so it is not held for this peer after the round.
	attempted := map[string]bool{}
	if p.selection != nil {
		want = p.selection.Select(ctx, pid.String(), resp.Files, want)
		selected := append([]FileManifest(nil), want...)
		defer func() {
			for _, f := range selected {
				if !attempted[f.ContentHash] {
					p.selection.Fetched(ctx, f, errNotAttempted)
				}
			}
		}()
	}
	deleted, err := p.files.deletedHashes(ctx)
	if err != nil {
		return err
	}
	// Everything still missing from this peer, before any backoff is applied.
	// A failure row for anything else is out of date — the content arrived from
	// another device, the owner deleted it, or the peer stopped offering it —
	// and is dropped, so the owner's list of stuck tracks stays one they can
	// act on rather than one they learn to distrust.
	stillWanted := make(map[string]bool, len(want))
	for _, file := range want {
		if !deleted[file.ContentHash] {
			stillWanted[file.ContentHash] = true
		}
	}
	p.failures.prune(ctx, pid.String(), stillWanted)

	// Selection, not attempt, is where a file that cannot be fetched is
	// dropped. The round is capped, so an entry that fails every time does not
	// merely waste its own attempt — it holds a slot a fetchable file needed,
	// and enough of them stall replication completely while the log fills with
	// the same errors.
	kept := want[:0]
	for _, file := range want {
		if deleted[file.ContentHash] {
			continue
		}
		// A name this filesystem cannot write is knowable before any network
		// work, so it is recognised here rather than discovered as a failed
		// write ten minutes into a transfer. A Windows device pulling from a
		// Linux or macOS peer meets these whenever the peer's library predates
		// portable naming.
		c := candidate{peerID: pid.String(), contentHash: file.ContentHash, relPath: file.RelPath}
		if !portablename.LocallyStorable(file.RelPath) {
			p.failures.noteUnattempted(ctx, c, ReasonUnstorablePath, unstorableDetail(file.RelPath))
			continue
		}
		if !p.failures.ready(ctx, c) {
			continue
		}
		kept = append(kept, file)
	}
	want = kept
	if len(want) == 0 {
		return nil
	}
	if len(want) > p.maxPerRound {
		want = want[:p.maxPerRound]
	}
	fetched := 0
	for _, f := range want {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := candidate{peerID: pid.String(), contentHash: f.ContentHash, relPath: f.RelPath}
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		attempted[f.ContentHash] = true
		err := p.files.FetchFileViaPeer(fetchCtx, p.host, pid.String(), f.RelPath, f.ContentHash)
		cancel()
		if p.selection != nil {
			p.selection.Fetched(ctx, f, err)
		}
		if err != nil {
			log.Printf("p2p pull: fetch %q from %s: %v", f.RelPath, pid, err)
			p.failures.recordAttempt(ctx, c, err)
			continue
		}
		// Backing off is not giving up: a file that starts succeeding forgets
		// its history, so a later failure begins its backoff from scratch.
		p.failures.clear(ctx, c)
		fetched++
	}
	if fetched > 0 {
		log.Printf("p2p pull: fetched %d file(s) from %s", fetched, pid)
	}
	return nil
}

// pullCovers fetches the bytes behind any cover this device has a row for but
// no image. A peer that does not have one either simply fails that fetch; the
// row stays, and the library backend's own art shows until some round succeeds.
func (p *Puller) pullCovers(ctx context.Context, pid peer.ID) {
	if p.covers == nil || p.coverDir == "" {
		return
	}
	rows, err := p.covers.ListEntityCovers(ctx)
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(rows))
	fetched, tried := 0, 0
	for _, r := range rows {
		// Bounded like the file lane: a peer that answers slowly must not make
		// one round run for as long as there are missing covers.
		if tried >= p.maxPerRound {
			break
		}
		if err := ctx.Err(); err != nil {
			return
		}
		ref := r.Sha256 + "." + r.Ext
		// One blob can back many entities; fetch it once.
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if _, err := os.Stat(coverBlobPath(p.coverDir, r.Sha256, r.Ext)); err == nil {
			continue
		}
		tried++
		fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		err := FetchCover(fetchCtx, p.host, p.coverDir, pid.String(), r.Sha256, r.Ext)
		cancel()
		if err == nil {
			fetched++
		}
	}
	if fetched > 0 {
		log.Printf("p2p pull: fetched %d cover(s) from %s", fetched, pid)
	}
}

func (p *Puller) localManifests(ctx context.Context) ([]FileManifest, error) {
	rows, err := p.store.ListFileManifests(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FileManifest, 0, len(rows))
	for _, r := range rows {
		out = append(out, FileManifest{
			CanonicalID: r.CanonicalID,
			ContentHash: r.ContentHash,
			Size:        r.Size,
			RelPath:     r.RelPath,
			Mtime:       r.Mtime,
			DeviceID:    r.DeviceID,
		})
	}
	return out, nil
}

// MissingFiles returns the remote entries this device should fetch: content we
// do not already hold, at a path that is free.
//
// The path check matters. FetchFileViaPeer renames the download over
// musicDir/relPath, so pulling a remote file whose path is already occupied by
// different local content would destroy that content. Those are left alone —
// the same path holding different bytes on two devices is a conflict this
// layer cannot resolve, and doing nothing is the recoverable outcome.
func MissingFiles(local, remote []FileManifest, musicDir string) []FileManifest {
	haveHash := make(map[string]bool, len(local))
	pathHash := make(map[string]string, len(local))
	for _, l := range local {
		if l.ContentHash != "" {
			haveHash[l.ContentHash] = true
		}
		if l.RelPath != "" {
			pathHash[l.RelPath] = l.ContentHash
		}
	}
	out := make([]FileManifest, 0)
	queued := make(map[string]bool)
	for _, r := range remote {
		if r.ContentHash == "" || r.RelPath == "" {
			continue
		}
		if haveHash[r.ContentHash] || queued[r.ContentHash] {
			continue
		}
		if _, err := validateRelPath(r.RelPath); err != nil {
			continue
		}
		// Occupied by different content, per the manifest.
		if h, ok := pathHash[r.RelPath]; ok && h != r.ContentHash {
			continue
		}
		// Occupied on disk but not in the manifest (scan not caught up yet).
		if musicDir != "" {
			if _, err := os.Stat(filepath.Join(musicDir, filepath.FromSlash(r.RelPath))); err == nil {
				continue
			}
		}
		queued[r.ContentHash] = true
		out = append(out, r)
	}
	return out
}
