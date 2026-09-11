// Package listenbrainz adapts ListenBrainz's public similarity datasets to the
// recommendation source contracts. The endpoints need no user account.
package listenbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/recommend"
)

const (
	defaultLabsURL        = "https://labs.api.listenbrainz.org"
	defaultMusicBrainzURL = "https://musicbrainz.org/ws/2"
	requestInterval       = time.Second
	recordingAlgorithm    = "session_based_days_7500_session_300_contribution_5_threshold_15_limit_50_skip_30"
	artistAlgorithm       = "session_based_days_7500_session_300_contribution_5_threshold_10_limit_100_filter_True_skip_30"
)

var (
	_ recommend.TrackSimilarity  = (*Source)(nil)
	_ recommend.ArtistSimilarity = (*Source)(nil)
)

// Source reads ListenBrainz's public derived datasets. A small per-instance
// limiter spaces every HTTP request, including metadata fallbacks, so Radio
// fan-out cannot burst at the community service.
type Source struct {
	labsURL        string
	musicBrainzURL string
	client         *http.Client
	interval       time.Duration
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error

	mu   sync.Mutex
	next time.Time
}

// New constructs the account-free production source.
func New() *Source {
	return &Source{
		labsURL: defaultLabsURL, musicBrainzURL: defaultMusicBrainzURL,
		client: http.DefaultClient, interval: requestInterval, now: time.Now, sleep: sleepContext,
	}
}

func newTestSource(labsURL, musicBrainzURL string, client *http.Client) *Source {
	return &Source{labsURL: labsURL, musicBrainzURL: musicBrainzURL, client: client, now: time.Now, sleep: sleepContext}
}

func (*Source) Name() string { return "listenbrainz" }

// SimilarTracks looks up the seed by MBID when possible, falling back to an
// exact artist/title match in ListenBrainz's recording search dataset.
func (s *Source) SimilarTracks(ctx context.Context, seed recommend.TrackSeed, limit int) ([]recommend.TrackCandidate, error) {
	mbid := strings.TrimSpace(seed.MBID)
	if mbid == "" {
		var err error
		mbid, err = s.recordingMBID(ctx, seed.Artist, seed.Title)
		if err != nil || mbid == "" {
			return nil, err
		}
	}

	q := url.Values{}
	q.Set("recording_mbids", mbid)
	q.Set("algorithm", recordingAlgorithm)
	var response []struct {
		MBID   string `json:"recording_mbid"`
		Title  string `json:"recording_name"`
		Artist string `json:"artist_credit_name"`
	}
	if err := s.getJSON(ctx, s.labsURL+"/similar-recordings/json?"+q.Encode(), &response); err != nil {
		return nil, err
	}
	if limit > 0 && len(response) > limit {
		response = response[:limit]
	}
	out := make([]recommend.TrackCandidate, 0, len(response))
	for _, item := range response {
		if item.Title == "" || item.Artist == "" {
			continue
		}
		out = append(out, recommend.TrackCandidate{Artist: item.Artist, Title: item.Title, MBID: item.MBID})
	}
	return out, nil
}

func (s *Source) recordingMBID(ctx context.Context, artist, title string) (string, error) {
	q := url.Values{}
	q.Set("query", strings.TrimSpace(matching.PrimaryArtist(artist)+" "+title))
	var response []struct {
		MBID   string `json:"recording_mbid"`
		Title  string `json:"recording_name"`
		Artist string `json:"artist_credit_name"`
	}
	if err := s.getJSON(ctx, s.labsURL+"/recording-search/json?"+q.Encode(), &response); err != nil {
		return "", err
	}
	want := recommend.TrackCandidate{Artist: artist, Title: title}
	for _, item := range response {
		if sameRecording(want, item.Artist, item.Title) {
			return item.MBID, nil
		}
	}
	return "", nil
}

// SimilarArtists uses an artist MBID directly or resolves an exact name via
// MusicBrainz before reading ListenBrainz's similar-artists dataset.
func (s *Source) SimilarArtists(ctx context.Context, seed recommend.ArtistSeed, limit int) ([]recommend.ArtistCandidate, error) {
	mbid := strings.TrimSpace(seed.MBID)
	if mbid == "" {
		var err error
		mbid, err = s.artistMBID(ctx, seed.Name)
		if err != nil || mbid == "" {
			return nil, err
		}
	}
	q := url.Values{}
	q.Set("artist_mbids", mbid)
	q.Set("algorithm", artistAlgorithm)
	var response []struct {
		MBID string `json:"artist_mbid"`
		Name string `json:"name"`
	}
	if err := s.getJSON(ctx, s.labsURL+"/similar-artists/json?"+q.Encode(), &response); err != nil {
		return nil, err
	}
	if limit > 0 && len(response) > limit {
		response = response[:limit]
	}
	out := make([]recommend.ArtistCandidate, 0, len(response))
	for _, item := range response {
		if item.Name != "" {
			out = append(out, recommend.ArtistCandidate{Source: s.Name(), ExternalID: item.MBID, Name: item.Name, MBID: item.MBID})
		}
	}
	return out, nil
}

func (s *Source) artistMBID(ctx context.Context, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	q := url.Values{}
	q.Set("query", `artist:"`+name+`"`)
	q.Set("fmt", "json")
	q.Set("limit", strconv.Itoa(10))
	var response struct {
		Artists []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
	}
	if err := s.getJSON(ctx, s.musicBrainzURL+"/artist/?"+q.Encode(), &response); err != nil {
		return "", err
	}
	want := matching.Normalize(name)
	for _, artist := range response.Artists {
		if matching.Normalize(artist.Name) == want {
			return artist.ID, nil
		}
	}
	return "", nil
}

func sameRecording(candidate recommend.TrackCandidate, artist, title string) bool {
	return matching.Normalize(candidate.Title) == matching.Normalize(title) && matching.ArtistMatches(artist, candidate.Artist)
}

func (s *Source) getJSON(ctx context.Context, endpoint string, out any) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("listenbrainz: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Reverb/dev (https://github.com/uhhhm/reverb)")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("listenbrainz: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("listenbrainz: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("listenbrainz: decode response: %w", err)
	}
	return nil
}

func (s *Source) wait(ctx context.Context) error {
	s.mu.Lock()
	now := s.now()
	start := now
	if s.next.After(start) {
		start = s.next
	}
	s.next = start.Add(s.interval)
	s.mu.Unlock()
	if delay := start.Sub(now); delay > 0 {
		return s.sleep(ctx, delay)
	}
	return nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
