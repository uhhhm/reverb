package recommend

import (
	"context"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
)

const (
	shelvesKey = "recommend.shelves"
	// shelfRefreshAfter is how old cached shelves get before a read starts a
	// background refresh.
	shelfRefreshAfter = 6 * time.Hour
	// shelfSeedWindow is what counts as recent heavy plays.
	shelfSeedWindow = 30 * 24 * time.Hour
	// lovedArtistWindow is the history "artists you love" are drawn from.
	lovedArtistWindow = 90 * 24 * time.Hour
	shelfSeeds        = 2
	// shelfSeedCandidates are the top recent plays searched for seeds by
	// different, unmarked artists.
	shelfSeedCandidates = 50
	lovedArtists        = 5
	moreFromArtists     = 3
	moreFromPerArtist   = 4
	shelfTracks         = 20
	shelfArtists        = 12
	shelfArtistCap      = 3
)

// ShelfKind names a Home shelf; the client words its title from it.
type ShelfKind string

const (
	// ShelfBecauseYouPlayed: "Because you played <Seed>", seeded from a recent
	// heavy play.
	ShelfBecauseYouPlayed ShelfKind = "becauseYouPlayed"
	// ShelfSimilarTo: "Similar to <Seed>", seeded from the library when there
	// are no plays yet.
	ShelfSimilarTo ShelfKind = "similarTo"
	// ShelfArtistsYouMightLike relates the most played (or collected) artists.
	ShelfArtistsYouMightLike ShelfKind = "artistsYouMightLike"
	// ShelfMoreFromArtists holds tracks by those artists not played lately.
	ShelfMoreFromArtists ShelfKind = "moreFromArtistsYouLove"
)

// Shelf is one "For you" row on Home. A shelf holds tracks or artists.
type Shelf struct {
	Kind    ShelfKind             `json:"kind"`
	Seed    *Seed                 `json:"seed,omitempty"`
	Tracks  []core.ExternalResult `json:"tracks"`
	Artists []core.ExternalArtist `json:"artists"`
}

// Shelves is Home's "For you" section as last generated on this device.
// Refreshing reports a background refresh, whose result a later read returns.
type Shelves struct {
	Shelves    []Shelf `json:"shelves"`
	UpdatedAt  int64   `json:"updatedAt,omitempty"`
	Offline    bool    `json:"offline,omitempty"`
	Refreshing bool    `json:"refreshing"`
}

// Shelves returns the cached shelves at once. Missing or old ones start a
// refresh in the background rather than holding up Home. Not interested
// marks apply to what was cached before they were made.
func (s *Service) Shelves(ctx context.Context) Shelves {
	var cached Shelves
	if !s.load(ctx, shelvesKey, &cached) || s.now().Sub(time.Unix(cached.UpdatedAt, 0)) >= shelfRefreshAfter {
		s.startRefresh(ctx, shelvesKey, func(ctx context.Context) { s.RefreshShelves(ctx) })
	}
	cached.Refreshing = s.isRefreshing(shelvesKey)
	cached.Shelves = s.withoutMarkedShelves(ctx, cached.Shelves)
	return cached
}

// RefreshShelves regenerates the shelves and stores them. When nothing can be
// generated (offline with an empty library, say) the last shelves stay, marked
// offline only if this refresh was, and a later read tries again.
func (s *Service) RefreshShelves(ctx context.Context) Shelves {
	fresh := s.buildShelves(ctx)
	var prev Shelves
	if len(fresh.Shelves) == 0 && s.load(ctx, shelvesKey, &prev) && len(prev.Shelves) > 0 {
		prev.Offline = fresh.Offline
		prev.Refreshing = false
		s.save(ctx, shelvesKey, prev)
		return prev
	}
	s.save(ctx, shelvesKey, fresh)
	return fresh
}

func (s *Service) withoutMarkedShelves(ctx context.Context, shelves []Shelf) []Shelf {
	ex := s.excluded(ctx)
	out := make([]Shelf, 0, len(shelves))
	for _, sh := range shelves {
		if sh.Seed != nil && markedSeed(ex, sh.Seed.Artist, sh.Seed.Title) {
			continue
		}
		sh.Tracks = s.withoutMarkedTracks(ctx, sh.Tracks)
		sh.Artists = s.withoutMarkedArtists(ctx, sh.Artists)
		if len(sh.Tracks)+len(sh.Artists) > 0 {
			out = append(out, sh)
		}
	}
	return out
}

func (s *Service) buildShelves(ctx context.Context) Shelves {
	now := s.now()
	out := Shelves{Shelves: []Shelf{}, UpdatedAt: now.Unix()}
	if s.listening == nil {
		return out
	}
	online := s.settings(ctx).Online && len(s.tracks) > 0
	out.Offline = !online
	profile := s.profile(ctx)
	recent := s.recentlyPlayed(ctx)
	seeds, kind, loved := s.shelfSeeds(ctx, now)

	for _, sd := range seeds {
		sh := Shelf{Kind: kind, Seed: &sd, Artists: []core.ExternalArtist{}}
		if online {
			tracks, available := s.fromSeeds(ctx, []Seed{sd}, []Seed{sd}, discoverySurface, recent, profile)
			sh.Tracks = firstN(capPerArtist(tracks, shelfArtistCap), shelfTracks)
			out.Offline = out.Offline || !available
		}
		if len(sh.Tracks) == 0 {
			// Offline, rediscovering the library beats an empty row.
			local := s.localTracks(ctx, TrackSeed{Artist: sd.Artist, Title: sd.Title})
			sh.Tracks = firstN(filterTracks(local.Tracks, []Seed{sd}, similarTracksSurface, recent), shelfTracks)
			out.Offline = out.Offline || len(sh.Tracks) > 0
		}
		if len(sh.Tracks) > 0 {
			out.Shelves = append(out.Shelves, sh)
		}
	}
	if artists, offline := s.artistsYouMightLike(ctx, loved, online, profile); len(artists) > 0 {
		out.Shelves = append(out.Shelves, Shelf{Kind: ShelfArtistsYouMightLike, Tracks: []core.ExternalResult{}, Artists: artists})
		out.Offline = out.Offline || offline
	}
	if tracks, offline := s.moreFromArtists(ctx, loved, online, recent); len(tracks) > 0 {
		out.Shelves = append(out.Shelves, Shelf{Kind: ShelfMoreFromArtists, Tracks: tracks, Artists: []core.ExternalArtist{}})
		out.Offline = out.Offline || offline
	}
	out.Shelves = s.withoutMarkedShelves(ctx, out.Shelves)
	return out
}

// shelfSeeds picks the shelves' seeds: the most played recent tracks by
// different artists and the most played artists. A new install with no plays
// seeds from the artists the library holds most of instead.
func (s *Service) shelfSeeds(ctx context.Context, now time.Time) ([]Seed, ShelfKind, []string) {
	ex := s.excluded(ctx)
	var seeds []Seed
	byArtist := map[string]bool{}
	if top, err := s.listening.TopTracks(ctx, now.Add(-shelfSeedWindow), now, shelfSeedCandidates); err == nil {
		for _, t := range top {
			if markedSeed(ex, t.Artist, t.Title) || byArtist[artistKey(t.Artist)] {
				continue
			}
			byArtist[artistKey(t.Artist)] = true
			seeds = append(seeds, Seed{Artist: t.Artist, Title: t.Title})
			if len(seeds) == shelfSeeds {
				break
			}
		}
	}
	var loved []string
	if top, err := s.listening.TopArtists(ctx, now.Add(-lovedArtistWindow), now, lovedArtists*2); err == nil {
		loved = unmarkedArtists(ex, top, lovedArtists)
	}
	if len(seeds) > 0 {
		return seeds, ShelfBecauseYouPlayed, loved
	}

	lib, err := s.listening.LibraryArtists(ctx, lovedArtists*2)
	if err != nil {
		return nil, ShelfSimilarTo, loved
	}
	libArtists := unmarkedArtists(ex, lib, lovedArtists)
	if len(loved) == 0 {
		loved = libArtists
	}
	for _, artist := range libArtists {
		tracks, err := s.listening.LibraryTracks(ctx, artist, 1)
		if err != nil || len(tracks) == 0 || markedSeed(ex, tracks[0].Artist, tracks[0].Title) {
			continue
		}
		seeds = append(seeds, Seed{Artist: tracks[0].Artist, Title: tracks[0].Title})
		if len(seeds) == shelfSeeds {
			break
		}
	}
	return seeds, ShelfSimilarTo, loved
}

func unmarkedArtists(ex Exclusions, counts []PlayedCount, n int) []string {
	var out []string
	for _, c := range counts {
		if !markedSeed(ex, c.Artist, "") {
			out = append(out, c.Artist)
		}
		if len(out) == n {
			break
		}
	}
	return out
}

// artistsYouMightLike relates every loved artist, interleaving their lists.
// Online, only artists the household has not played are kept; offline, the
// library's related artists stand in.
func (s *Service) artistsYouMightLike(ctx context.Context, loved []string, online bool, profile *Profile) ([]core.ExternalArtist, bool) {
	lists := make([][]core.ExternalArtist, len(loved))
	fellBack := make([]bool, len(loved))
	var wg sync.WaitGroup
	for i, name := range loved {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if online {
				for _, a := range s.similarArtistsByName(ctx, name).Artists {
					if !profile.PlayedArtist(a.Name) {
						lists[i] = append(lists[i], a)
					}
				}
			}
			if len(lists[i]) == 0 {
				lists[i] = s.localArtists(ctx, ArtistSeed{Name: name}).Artists
				fellBack[i] = len(lists[i]) > 0
			}
		}()
	}
	wg.Wait()

	skip := map[string]bool{}
	for _, name := range loved {
		skip[matching.Normalize(name)] = true
	}
	var out []core.ExternalArtist
	for _, a := range interleave(lists) {
		if key := matching.Normalize(a.Name); !skip[key] {
			skip[key] = true
			out = append(out, a)
		}
	}
	offline := false
	for _, f := range fellBack {
		offline = offline || f
	}
	return firstN(out, shelfArtists), offline
}

// moreFromArtists interleaves a few tracks by each loved artist, leaving out
// what was played lately and other versions. Online they come from the search
// sources (as the library copy where owned); offline, from the library.
func (s *Service) moreFromArtists(ctx context.Context, loved []string, online bool, recent map[string]bool) ([]core.ExternalResult, bool) {
	loved = firstN(loved, moreFromArtists)
	lists := make([][]core.ExternalResult, len(loved))
	offline := false
	for i, name := range loved {
		var tracks []core.ExternalResult
		if online {
			tracks = s.artistTracks(ctx, name, moreFromPerArtist*3)
		}
		if len(tracks) == 0 {
			tracks, _ = s.listening.LibraryTracks(ctx, name, moreFromPerArtist*5)
			offline = offline || len(tracks) > 0
		}
		for _, t := range tracks {
			if recent[recordingKey(t.Title, t.Artist)] || versionKinds(t.Title) != 0 {
				continue
			}
			t.Reason = &core.RecommendationReason{Kind: core.ReasonMoreFrom, Artist: name}
			lists[i] = append(lists[i], t)
			if len(lists[i]) == moreFromPerArtist {
				break
			}
		}
	}
	seen := map[string]bool{}
	var out []core.ExternalResult
	for _, t := range interleave(lists) {
		if key := recordingKey(t.Title, t.Artist); !seen[key] {
			seen[key] = true
			out = append(out, t)
		}
	}
	return out, offline
}
