// internal/coverage/rollup.go
package coverage

import (
	"context"
	"errors"
	"sync"

	"github.com/uhhhm/reverb/internal/core"
)

// rollupTrackConcurrency caps concurrent Match calls inside a single RollUp.
// 8 keeps a 15-track album well under a second even when each Match does
// 1-2 library searches, without flooding the DB with hundreds of concurrent
// queries when StreamCoverage fans out across albums.
const rollupTrackConcurrency = 8

// Matcher is the slice of *matching.Service the rollup needs.
type Matcher interface {
	Match(ctx context.Context, ext core.ExternalResult) (core.MatchResult, error)
}

// RollUp computes exact coverage for one album. full iff every track matches.
// LibraryAlbumID is left empty here; the service (Task 6) backfills it from
// library lookups because MatchResult carries only the track id.
// Track matching is concurrent (bounded) to avoid N serial library searches
// per album — a 15-track album with 2 searches/track goes from ~15× RTT
// to ~2× RTT.
func RollUp(ctx context.Context, m Matcher, al core.ExternalAlbum) (core.AlbumCoverage, error) {
	cov := core.AlbumCoverage{
		Source:          al.Source,
		ExternalAlbumID: al.ExternalID,
		TotalCount:      len(al.Tracks),
		MissingTracks:   []core.ExternalTrackRef{},
	}
	if len(al.Tracks) == 0 {
		cov.State = core.CoverageNone
		return cov, nil
	}
	// Fast path for single track — avoid goroutine overhead and preserve
	// original error semantics without concurrency.
	if len(al.Tracks) == 1 {
		tr := al.Tracks[0]
		res, err := m.Match(ctx, tr)
		if err != nil {
			return core.AlbumCoverage{}, err
		}
		if res.Status == core.MatchInLibrary && res.LibraryTrackID != "" {
			cov.OwnedCount = 1
			cov.State = core.CoverageFull
		} else {
			cov.MissingTracks = []core.ExternalTrackRef{{
				Source: tr.Source, ExternalID: tr.ExternalID, Title: tr.Title,
				Artist: tr.Artist, Album: al.Name, ISRC: tr.ISRC, DurationMs: tr.DurationMs,
			}}
			cov.State = core.CoverageNone
		}
		return cov, nil
	}

	type trackResult struct {
		res    core.MatchResult
		err    error
		source string
		extID  string
		title  string
		artist string
		isrc   string
		dur    int
	}
	results := make([]trackResult, len(al.Tracks))
	sem := make(chan struct{}, rollupTrackConcurrency)
	var wg sync.WaitGroup
	for i, tr := range al.Tracks {
		wg.Add(1)
		go func(idx int, tr core.ExternalResult) {
			defer wg.Done()
			// Bounded concurrency with context cancellation.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[idx] = trackResult{err: ctx.Err()}
				return
			}
			r, err := m.Match(ctx, tr)
			results[idx] = trackResult{
				res: r, err: err,
				source: tr.Source, extID: tr.ExternalID, title: tr.Title,
				artist: tr.Artist, isrc: tr.ISRC, dur: tr.DurationMs,
			}
		}(i, tr)
	}
	wg.Wait()
	// Prefer a real Match error over a concurrent context cancellation so a DB
	// failure is not masked when cancellation races with it.
	for _, r := range results {
		if r.err != nil && !errors.Is(r.err, context.Canceled) && !errors.Is(r.err, context.DeadlineExceeded) {
			return core.AlbumCoverage{}, r.err
		}
	}
	if err := ctx.Err(); err != nil {
		return core.AlbumCoverage{}, err
	}
	for _, r := range results {
		if r.err != nil {
			return core.AlbumCoverage{}, r.err
		}
		if r.res.Status == core.MatchInLibrary && r.res.LibraryTrackID != "" {
			cov.OwnedCount++
		} else {
			cov.MissingTracks = append(cov.MissingTracks, core.ExternalTrackRef{
				Source: r.source, ExternalID: r.extID, Title: r.title,
				Artist: r.artist, Album: al.Name, ISRC: r.isrc, DurationMs: r.dur,
			})
		}
	}
	switch {
	case cov.TotalCount > 0 && cov.OwnedCount == cov.TotalCount:
		cov.State = core.CoverageFull
	case cov.OwnedCount == 0:
		cov.State = core.CoverageNone
	default:
		cov.State = core.CoveragePartial
	}
	return cov, nil
}
