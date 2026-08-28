package p2p

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
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
	for pid := range trusted {
		if pid == p.host.ID() {
			continue
		}
		if !p.ensureConnected(ctx, pid) {
			continue
		}
		if err := p.pullPeer(ctx, pid); err != nil {
			log.Printf("p2p pull: peer %s: %v", pid, err)
		}
	}
}

// ensureConnected dials a paired peer we are not currently connected to, using
// whatever addresses discovery has already put in the peerstore. Without this
// a peer that dropped its connection is never synced again until mDNS happens
// to rediscover it.
func (p *Puller) ensureConnected(ctx context.Context, pid peer.ID) bool {
	if len(p.host.Network().ConnsToPeer(pid)) > 0 {
		return true
	}
	addrs := p.host.Peerstore().Addrs(pid)
	if len(addrs) == 0 {
		return false
	}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := p.host.Connect(dialCtx, peer.AddrInfo{ID: pid, Addrs: addrs}); err != nil {
		return false
	}
	return true
}

func (p *Puller) pullPeer(ctx context.Context, pid peer.ID) error {
	resp, err := RequestManifest(ctx, p.host, pid)
	if err != nil {
		return err
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
	p.guard.Touch(ctx, pid)
	return nil
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
