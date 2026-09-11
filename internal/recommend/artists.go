package recommend

import (
	"context"
	"log"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/search"
)

// similarArtistLimit is how many related artists an artist page shows.
const similarArtistLimit = 10

// ArtistResult is the "Fans also like" section of an artist page. Available is
// false when no configured source can relate artists, and the section is then
// hidden; an empty list with Available set means the lookup found nothing or
// failed.
type ArtistResult struct {
	Available bool                  `json:"available"`
	Artists   []core.ExternalArtist `json:"artists"`
}

type similarArtistSource struct {
	search.SearchSource
	provider search.SimilarArtistsProvider
}

// SimilarArtists returns artists related to one on a source or in the library.
// A library artist is looked up by name in each capable source in turn.
func (s *Service) SimilarArtists(ctx context.Context, source, id string) ArtistResult {
	var capable []similarArtistSource
	for _, src := range s.liveSources() {
		if p, ok := src.(search.SimilarArtistsProvider); ok {
			capable = append(capable, similarArtistSource{src, p})
		}
	}
	if len(capable) == 0 {
		return ArtistResult{Artists: []core.ExternalArtist{}}
	}
	result := ArtistResult{Available: true, Artists: []core.ExternalArtist{}}

	key := "artists\x1f" + source + "\x1f" + id
	if cached, ok := s.cache.get(key); ok {
		result.Artists = s.withoutMarkedArtists(ctx, cached.([]core.ExternalArtist))
		return result
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	src, externalID := s.resolveArtist(ctx, source, id, capable)
	if src.provider == nil {
		return result
	}
	artists, err := src.provider.SimilarArtists(ctx, externalID, similarArtistLimit)
	if err != nil {
		log.Printf("recommend: similar artists for %s/%s: %v", src.Name(), externalID, err)
		return result
	}
	if len(artists) > similarArtistLimit {
		artists = artists[:similarArtistLimit]
	}
	s.cache.put(key, artists)
	result.Artists = s.withoutMarkedArtists(ctx, artists)
	return result
}

// resolveArtist names the capable source and its artist id to ask about. A
// source artist is asked about directly; a library artist is found by exact
// normalised name, never by the top hit, which would relate the wrong artist.
func (s *Service) resolveArtist(ctx context.Context, source, id string, capable []similarArtistSource) (similarArtistSource, string) {
	if source != "library" {
		for _, src := range capable {
			if src.Name() == source {
				return src, id
			}
		}
		return similarArtistSource{}, ""
	}
	lib := s.liveLibrary()
	if lib == nil {
		return similarArtistSource{}, ""
	}
	artist, err := lib.GetArtist(ctx, id)
	if err != nil || artist.Name == "" {
		return similarArtistSource{}, ""
	}
	want := matching.Normalize(artist.Name)
	for _, src := range capable {
		hits, err := src.Search(ctx, artist.Name, core.EntityArtist)
		if err != nil {
			continue
		}
		for _, hit := range hits {
			if matching.Normalize(hit.Title) == want {
				return src, hit.ExternalID
			}
		}
	}
	return similarArtistSource{}, ""
}
