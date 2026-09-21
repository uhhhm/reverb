package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/uhhhm/reverb/internal/player"
)

// maxPlayerBody bounds a queue request. Playing a whole library sends every
// track, so it is large, but not unbounded.
const maxPlayerBody = 32 << 20

type playerTracksRequest struct {
	Tracks []json.RawMessage `json:"tracks"`
	Origin player.Origin     `json:"origin"`
	// Start is the position to start at, for play.
	Start int `json:"start"`
}

type playerEntryRequest struct {
	// EntryID names the entry the player was on. When it is no longer current
	// the request is stale — the listener moved on first — and changes nothing.
	EntryID string `json:"entryId"`
}

// routePlayer mounts the queue API: one queue per player session.
func (s *Server) routePlayer(r chi.Router) {
	r.Get("/player/{session}", s.handlePlayerState)
	r.Post("/player/{session}/play", s.playerTracks(func(q *player.Queue, b playerTracksRequest) error {
		return q.Play(b.Tracks, b.Start, b.Origin)
	}))
	r.Post("/player/{session}/enqueue", s.playerTracks(func(q *player.Queue, b playerTracksRequest) error {
		return q.Enqueue(b.Tracks, b.Origin)
	}))
	r.Post("/player/{session}/remove", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Positions []int `json:"positions"`
		}
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { q.Remove(body.Positions); return nil })
	})
	r.Post("/player/{session}/move", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			From int `json:"from"`
			To   int `json:"to"`
		}
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { q.Move(body.From, body.To); return nil })
	})
	r.Post("/player/{session}/jump", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Index int `json:"index"`
		}
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { q.Jump(body.Index); return nil })
	})
	r.Post("/player/{session}/next", s.playerEntry((*player.Queue).Next))
	r.Post("/player/{session}/previous", s.playerEntry((*player.Queue).Previous))
	r.Post("/player/{session}/ended", s.playerEntry((*player.Queue).Ended))
	r.Post("/player/{session}/shuffle", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			On bool `json:"on"`
		}
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { q.SetShuffle(body.On); return nil })
	})
	r.Post("/player/{session}/repeat", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Mode player.Repeat `json:"mode"`
		}
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { return q.SetRepeat(body.Mode) })
	})
	r.Post("/player/{session}/radio-ended", func(w http.ResponseWriter, r *http.Request) {
		s.playerUpdate(w, r, nil, func(q *player.Queue) error { q.RadioEnded(); return nil })
	})
	r.Post("/player/{session}/clear", func(w http.ResponseWriter, r *http.Request) {
		s.playerUpdate(w, r, nil, func(q *player.Queue) error { q.Clear(); return nil })
	})
}

func (s *Server) handlePlayerState(w http.ResponseWriter, r *http.Request) {
	if s.deps.Player == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "player unavailable"})
		return
	}
	st, err := s.deps.Player.State(chi.URLParam(r, "session"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) playerTracks(change func(*player.Queue, playerTracksRequest) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body playerTracksRequest
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { return change(q, body) })
	}
}

// playerEntry wraps a move that only applies while the entry the player named
// is still current.
func (s *Server) playerEntry(move func(*player.Queue)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body playerEntryRequest
		s.playerUpdate(w, r, &body, func(q *player.Queue) error {
			if body.EntryID == "" || body.EntryID == q.Current() {
				move(q)
			}
			return nil
		})
	}
}

// playerUpdate decodes body (when non-nil and the request has one), applies
// change to the session's queue, and replies with the queue.
func (s *Server) playerUpdate(w http.ResponseWriter, r *http.Request, body any, change func(*player.Queue) error) {
	if s.deps.Player == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "player unavailable"})
		return
	}
	if body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlayerBody)).Decode(body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
	}
	st, err := s.deps.Player.Update(chi.URLParam(r, "session"), change)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, player.ErrTooLong) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
}
