package api

import (
	"context"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ExternalStreamResolver turns a source+external id into a direct audio URL.
// *extstream.Service fits. Nil when no search source is configured.
type ExternalStreamResolver interface {
	Resolve(ctx context.Context, source, externalID string) (string, error)
	Invalidate(source, externalID string)
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
	source, id := chi.URLParam(r, "source"), chi.URLParam(r, "id")

	url, err := s.deps.ExternalStream.Resolve(r.Context(), source, id)
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
		if url, err = s.deps.ExternalStream.Resolve(r.Context(), source, id); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if resp, err = s.fetchExternalAudio(r, url); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
	}
	defer resp.Body.Close()

	h := w.Header()
	for _, k := range []string{"Content-Type", "Content-Length", "Accept-Ranges", "Content-Range"} {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "audio/mpeg")
	}
	// Resolved URLs are short-lived and listener-specific; never let a shared
	// cache hold on to one.
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// fetchExternalAudio issues the upstream GET, forwarding the browser's Range so
// seeking works and playback can start before the whole file arrives.
func (s *Server) fetchExternalAudio(r *http.Request, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if rng := r.Header.Get("Range"); rng != "" {
		req.Header.Set("Range", rng)
	}
	return extStreamClient.Do(req)
}
