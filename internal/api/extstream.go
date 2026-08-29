package api

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uhhhm/reverb/internal/trackref"
)

// ExternalStreamResolver turns a source+external id into a direct audio URL.
// *extstream.Service fits. Nil when no search source is configured.
type ExternalStreamResolver interface {
	// ResolveHinted takes the artist/title the caller already knows; empty hints
	// make it fall back to looking the track up at the search source.
	ResolveHinted(ctx context.Context, source, externalID, artist, title string) (string, error)
	Invalidate(source, externalID string)
}

// isExternalTrackID reports whether id names a track that is not in the library.
// The SPA synthesises "<source>:<externalId>" for such rows; real backend track
// ids never contain a colon. Delegates to trackref so the heuristic is owned in
// one place.
func isExternalTrackID(id string) bool {
	return trackref.IsExternalID(id)
}

// extStreamClient has no timeout: the response body is a whole audio stream that
// is copied for as long as the listener plays it. Redirects are followed (the
// upstream commonly issues one), and resolve-side timeouts live in extstream.
var extStreamClient = &http.Client{}

// handleExternalStream plays a search result that is not in the library, without
// downloading it: the track is resolved to a direct audio URL and proxied here,
// so no file is written, no download job is created, and no library scan runs.
// Adding the track to the library stays a separate, explicit action.
//
// The inbound Range is forwarded upstream and the response headers copied back,
// which is what makes the audio progressive and seekable rather than a wait for
// a complete file.
func (s *Server) handleExternalStream(w http.ResponseWriter, r *http.Request) {
	if s.deps.ExternalStream == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "external streaming unavailable"})
		return
	}
	s.serveExternalStream(w, r,
		chi.URLParam(r, "source"), chi.URLParam(r, "id"),
		r.URL.Query().Get("artist"), r.URL.Query().Get("title"))
}

// serveExternalStream resolves source+id to a direct audio URL and proxies it.
// Shared by the external-stream route and the canonical-id stream path, which
// falls back here for a track that was played from a source and has no copy in
// the library.
func (s *Server) serveExternalStream(w http.ResponseWriter, r *http.Request, source, id, artist, title string) {
	url, err := s.deps.ExternalStream.ResolveHinted(r.Context(), source, id, artist, title)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	resp, err := s.fetchExternalAudio(r, url)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	// A resolved URL can expire ahead of its cache TTL, which the upstream
	// reports as 403/410. Drop it and resolve once more rather than handing the
	// listener a dead stream.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone {
		_ = resp.Body.Close()
		s.deps.ExternalStream.Invalidate(source, id)
		if url, err = s.deps.ExternalStream.ResolveHinted(r.Context(), source, id, artist, title); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if resp, err = s.fetchExternalAudio(r, url); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	defer resp.Body.Close()

	status, dropContentRange := normalizeRangeless(r.Header.Get("Range"), resp)

	h := w.Header()
	for _, k := range []string{"Content-Type", "Content-Length", "Accept-Ranges", "Content-Range"} {
		if k == "Content-Range" && dropContentRange {
			continue
		}
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	// Seeking is what the player does most on a long track, and it only offers
	// it when the stream advertises range support.
	if h.Get("Accept-Ranges") == "" {
		h.Set("Accept-Ranges", "bytes")
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "audio/mpeg")
	}
	// Resolved URLs are short-lived and listener-specific; never let a shared
	// cache hold on to one.
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.Copy(w, resp.Body)
}

// fetchExternalAudio issues the upstream GET, forwarding the browser's Range so
// seeking works and playback can start before the whole file arrives.
//
// A range-less GET is never issued. The upstream throttles those to roughly
// real-time playback speed — measured at ~7 KB/s against ~4 MB/s for the very
// same URL requested as "bytes=0-" — which is what made a track take tens of
// seconds to start and then stall every few seconds. When the browser sends no
// Range (its first, metadata-probing request usually doesn't), an open-ended one
// is substituted; normalizeRangeless then hands the browser back the plain 200
// it asked for.
func (s *Server) fetchExternalAudio(r *http.Request, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	} else {
		req.Header.Set("Range", "bytes=0-")
	}
	return extStreamClient.Do(req)
}

// normalizeRangeless turns the 206 produced by the substituted "bytes=0-" back
// into the 200 a browser that sent no Range expects. A client that never asked
// for a range must not be told it received partial content, and Content-Range
// would be meaningless to it.
//
// Only a response covering the whole resource is downgraded; anything else is
// left alone so a genuine partial is never misreported as complete.
func normalizeRangeless(clientRange string, resp *http.Response) (status int, dropContentRange bool) {
	if clientRange != "" || resp.StatusCode != http.StatusPartialContent {
		return resp.StatusCode, false
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Range"), "bytes 0-") {
		return resp.StatusCode, false
	}
	return http.StatusOK, true
}

// handleExternalStreamPrewarm resolves a track's audio URL and caches it,
// without fetching any audio. The resolve is the multi-second part of playing an
// external track, so the SPA fires this as soon as a track becomes likely to
// play — the row is hovered, or it is the next item in the queue — and the
// resolve then overlaps the user's own click instead of following it.
func (s *Server) handleExternalStreamPrewarm(w http.ResponseWriter, r *http.Request) {
	if s.deps.ExternalStream == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "external streaming unavailable"})
		return
	}
	source, id := chi.URLParam(r, "source"), chi.URLParam(r, "id")
	artist, title := r.URL.Query().Get("artist"), r.URL.Query().Get("title")
	if _, err := s.deps.ExternalStream.ResolveHinted(r.Context(), source, id, artist, title); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
