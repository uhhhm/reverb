package p2p

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
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
	return &Puller{
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
	}
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
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pullAll(ctx)
		}
	}
}

func (p *Puller) pullAll(ctx context.Context) {
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
			if !EnsureConnected(ctx, p.host, p.guard, pid) {
				return
			}
			if err := p.pullPeer(ctx, pid); err != nil {
				log.Printf("p2p pull: peer %s: %v", pid, err)
			}
		}()
	}
	wg.Wait()
}

func (p *Puller) pullPeer(ctx context.Context, pid peer.ID) error {
	resp, err := RequestManifest(ctx, p.host, pid)
	if err != nil {
		return err
	}
	// A round that got this far proves the current address works; persist it so
	// the next restart can dial the peer without discovery.
	if err := p.guard.RememberAddrs(ctx, pid, ObservedAddrs(p.host, pid)); err != nil {
		log.Printf("p2p pull: remember addrs for %s: %v", pid, err)
	}
	if len(resp.Files) == 0 {
		p.guard.Touch(ctx, pid)
		return nil
	}
	local, err := p.localManifests(ctx)
	if err != nil {
		return err
	}
	want := MissingFiles(local, resp.Files, p.musicDir)
	if len(want) == 0 {
		p.guard.Touch(ctx, pid)
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
		fetchCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		err := p.files.FetchFileViaPeer(fetchCtx, p.host, pid.String(), f.RelPath, f.ContentHash)
		cancel()
		if err != nil {
			log.Printf("p2p pull: fetch %q from %s: %v", f.RelPath, pid, err)
			continue
		}
		fetched++
	}
	if fetched > 0 {
		log.Printf("p2p pull: fetched %d file(s) from %s", fetched, pid)
	}
	p.pullCovers(ctx, pid)
	p.guard.Touch(ctx, pid)
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
