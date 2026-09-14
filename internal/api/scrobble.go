package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/uhhhm/reverb/internal/scrobble"
)

// handleScrobbleAuthURL serves POST /api/v1/scrobble/lastfm/auth-url.
// Starts the Last.fm OAuth-style token flow. On error it distinguishes the two
// failure modes so the FE can react correctly:
//   - app API key/secret unset → 400 {"error":"lastfm_not_configured"} (admin must configure)
//   - configured but the provider call failed → 502 {"error":"lastfm_unavailable"} (transient)
//
// Returns {"authUrl","token"} on success.
func (s *Server) handleScrobbleAuthURL(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scrobble == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scrobble service unavailable"})
		return
	}

	authURL, token, err := s.deps.Scrobble.AuthURL(r.Context())
	if err != nil {
		if !s.deps.Scrobble.IsConfigured() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "lastfm_not_configured"})
		} else {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "lastfm_unavailable"})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"authUrl": authURL,
		"token":   token,
	})
}

// handleScrobbleComplete serves POST /api/v1/scrobble/lastfm/complete.
// Exchanges the approved token for a session key, stores the link, and returns
// the Last.fm username. Body: {"token": "<lastfm_token>"}.
func (s *Server) handleScrobbleComplete(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scrobble == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scrobble service unavailable"})
		return
	}
	cu, ok := currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var in struct {
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil || in.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	username, err := s.deps.Scrobble.CompleteAuth(r.Context(), cu.ID, in.Token)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": username})
}

// handleScrobbleUnlink serves DELETE /api/v1/scrobble/lastfm.
// Removes the session user's Last.fm link. Returns 204.
func (s *Server) handleScrobbleUnlink(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scrobble == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scrobble service unavailable"})
		return
	}
	cu, ok := currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := s.deps.Scrobble.Unlink(r.Context(), cu.ID, "lastfm"); err != nil {
		log.Printf("scrobble: unlink user=%s: %v", cu.ID, err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// linksResponse is the JSON shape for GET /scrobble/links.
// session_key is intentionally absent.
type linksResponse struct {
	Configured bool       `json:"configured"`
	Links      []linkJSON `json:"links"`
}

type linkJSON struct {
	Provider string `json:"provider"`
	Username string `json:"username"`
	Status   string `json:"status"`
}

// handleScrobbleLinks serves GET /api/v1/scrobble/links.
// Returns the session user's provider links plus a "configured" flag that
// indicates whether the app-level api_key + api_secret are present.
// session_key is NEVER included in any link object.
func (s *Server) handleScrobbleLinks(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scrobble == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scrobble service unavailable"})
		return
	}
	cu, ok := currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	links, err := s.deps.Scrobble.Links(r.Context(), cu.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	out := linksResponse{
		Configured: s.deps.Scrobble.IsConfigured(),
		Links:      make([]linkJSON, 0, len(links)),
	}
	for _, l := range links {
		out.Links = append(out.Links, linkJSON{
			Provider: l.Provider,
			Username: l.Username,
			Status:   l.Status,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleScrobbleNowPlaying serves POST /api/v1/scrobble/nowplaying.
// Fire-and-forget: always returns 204 regardless of errors or link state.
// Body: {"title","artist","album","durationMs"} (lowercase json tags).
func (s *Server) handleScrobbleNowPlaying(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scrobble == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scrobble service unavailable"})
		return
	}
	cu, ok := currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var in struct {
		Title      string `json:"title"`
		Artist     string `json:"artist"`
		Album      string `json:"album"`
		DurationMs int    `json:"durationMs"`
	}
	if err := decode(r, &in); err != nil {
		// fire-and-forget — still return 204 even on decode error
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.deps.Scrobble.NowPlaying(r.Context(), cu.ID, scrobble.Track{
		Title:      in.Title,
		Artist:     in.Artist,
		Album:      in.Album,
		DurationMs: in.DurationMs,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleListenBrainzConnect serves PUT /api/v1/scrobble/listenbrainz.
// Body: {"token": "<ListenBrainz user token>"}. The token is checked with
// ListenBrainz, then stored like a Last.fm session key: it is never returned
// or logged. Uploads are opt-in, so nothing is sent until this succeeds.
// Returns {"username"}; 400 {"error":"invalid_token"} when ListenBrainz
// rejects the token, 502 {"error":"listenbrainz_unavailable"} when it cannot
// be reached, and 503 when ListenBrainz uploads are not wired in.
func (s *Server) handleListenBrainzConnect(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scrobble == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scrobble service unavailable"})
		return
	}
	cu, ok := currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	username, err := s.deps.Scrobble.ConnectToken(r.Context(), cu.ID, scrobble.ListenBrainz, in.Token)
	switch {
	case errors.Is(err, scrobble.ErrAuth):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_token"})
		return
	case errors.Is(err, scrobble.ErrUnknownProvider):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "listenbrainz uploads unavailable"})
		return
	case err != nil:
		log.Printf("scrobble: listenbrainz connect user=%s: %v", cu.ID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "listenbrainz_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": username})
}

// handleListenBrainzUnlink serves DELETE /api/v1/scrobble/listenbrainz.
// Removes the link and its pending uploads. Returns 204.
func (s *Server) handleListenBrainzUnlink(w http.ResponseWriter, r *http.Request) {
	if s.deps.Scrobble == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "scrobble service unavailable"})
		return
	}
	cu, ok := currentUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if err := s.deps.Scrobble.Unlink(r.Context(), cu.ID, scrobble.ListenBrainz); err != nil {
		log.Printf("scrobble: listenbrainz unlink user=%s: %v", cu.ID, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not disconnect"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
