package recommend

import (
	"context"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/core"
)

const (
	// refreshRetry spaces out background refreshes of one surface, so one that
	// fails (offline, a source down) is not retried on every read.
	refreshRetry = 30 * time.Minute
	// refreshTimeout bounds one background refresh: a Mix looks up several
	// seeds and every candidate, so it gets far longer than one lookup.
	refreshTimeout = 3 * time.Minute
	// everyRecording is a limit no play history reaches, for reads that need
	// every recording in a window.
	everyRecording = 1 << 20
)

// fromSeeds gathers tracks similar to every lookup seed, interleaved so each
// seed's best come first. The surface's filters then apply, with exclude as
// the recordings never recommended back, and the rest are ranked by support
// across the seeds and the taste profile. A recording proposed for several
// seeds gathers support from each, and its reason names the seed that ranked
// it highest. available reports whether any source could be asked.
func (s *Service) fromSeeds(ctx context.Context, lookup, exclude []Seed, policy surfacePolicy, recent map[string]bool, profile *Profile) ([]core.ExternalResult, bool) {
	lists := make([]TrackResult, len(lookup))
	var wg sync.WaitGroup
	for i, sd := range lookup {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i] = s.similarTracks(ctx, TrackSeed{Artist: sd.Artist, Title: sd.Title, MBID: sd.MBID})
		}()
	}
	wg.Wait()

	available := false
	trackLists := make([][]core.ExternalResult, len(lists))
	for i, l := range lists {
		available = available || l.Available
		trackLists[i] = l.Tracks
	}
	merged := interleave(trackLists)

	support := map[string]float64{}
	sources := map[string][]string{}
	best := map[string]float64{}
	reasonSeed := map[string]Seed{}
	for i, l := range lists {
		for pos, t := range l.Tracks {
			key := recordingKey(t.Title, t.Artist)
			v := supportAt(pos)
			support[key] += v
			sources[key] = appendUnique(sources[key], t.RecommendationSources...)
			if v > best[key] {
				best[key], reasonSeed[key] = v, lookup[i]
			}
		}
	}
	out := filterTracks(merged, exclude, policy, recent)
	for i := range out {
		key := recordingKey(out[i].Title, out[i].Artist)
		out[i].RecommendationSources = sources[key]
		out[i].Reason = trackReason(reasonSeed[key], profile)
	}
	rankTracks(out, support, profile)
	return out, available
}

// capPerArtist keeps at most n tracks per primary artist, in order.
func capPerArtist(tracks []core.ExternalResult, n int) []core.ExternalResult {
	count := map[string]int{}
	out := make([]core.ExternalResult, 0, len(tracks))
	for _, t := range tracks {
		key := artistKey(t.Artist)
		if count[key] < n {
			count[key]++
			out = append(out, t)
		}
	}
	return out
}

// interleave takes the first item of every list, then the second, and so on.
func interleave[T any](lists [][]T) []T {
	var out []T
	for i := 0; ; i++ {
		more := false
		for _, l := range lists {
			if i < len(l) {
				out = append(out, l[i])
				more = true
			}
		}
		if !more {
			return out
		}
	}
}

func firstN[T any](items []T, n int) []T {
	if len(items) > n {
		return items[:n]
	}
	return items
}

// seededOrder orders items by a hash of the period seed and each item's key,
// so each period picks a stable selection of its own from the same pool.
func seededOrder[T any](items []T, seed string, key func(T) string) {
	hashes := make(map[string]uint64, len(items))
	for _, item := range items {
		k := key(item)
		h := fnv.New64a()
		h.Write([]byte(seed))
		h.Write([]byte{0x1f})
		h.Write([]byte(k))
		hashes[k] = h.Sum64()
	}
	sort.SliceStable(items, func(i, j int) bool { return hashes[key(items[i])] < hashes[key(items[j])] })
}

// startRefresh runs fn in the background unless a refresh under key is
// running or was started within refreshRetry. The refresh outlives the
// request that started it.
func (s *Service) startRefresh(ctx context.Context, key string, fn func(context.Context)) {
	s.refreshMu.Lock()
	last, tried := s.attempts[key]
	if s.refreshing[key] || tried && s.now().Sub(last) < refreshRetry {
		s.refreshMu.Unlock()
		return
	}
	s.refreshing[key] = true
	s.attempts[key] = s.now()
	s.refreshMu.Unlock()
	s.background(func() {
		defer func() {
			s.refreshMu.Lock()
			delete(s.refreshing, key)
			s.refreshMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		defer cancel()
		fn(ctx)
	})
}

func (s *Service) isRefreshing(key string) bool {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	return s.refreshing[key]
}

// playedBetween returns the recording keys played in [since, until).
func (s *Service) playedBetween(ctx context.Context, since, until time.Time) (map[string]bool, error) {
	out := map[string]bool{}
	if s.listening == nil {
		return out, nil
	}
	played, err := s.listening.TopTracks(ctx, since, until, everyRecording)
	if err != nil {
		return nil, err
	}
	for _, p := range played {
		out[recordingKey(p.Title, p.Artist)] = true
	}
	return out, nil
}

// markedSeed reports whether a seed track or its artist is marked Not
// interested.
func markedSeed(ex Exclusions, artist, title string) bool {
	if ex == nil {
		return false
	}
	if ex.Artist(artist) {
		return true
	}
	return title != "" && ex.Track(core.ExternalResult{Artist: artist, Title: title, Type: core.EntityTrack})
}
