package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/uhhhm/reverb/internal/player"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/trackref"
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
	r.Post("/player/{session}/radio", func(w http.ResponseWriter, r *http.Request) {
		var body player.RadioStart
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { return q.StartRadio(body) })
	})
	r.Post("/player/{session}/progress", func(w http.ResponseWriter, r *http.Request) {
		var body player.Progress
		s.playerUpdate(w, r, &body, func(q *player.Queue) error { q.Progress(body); return nil })
	})
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
		// Entry filter is optional: no body means apply unconditionally.
		if r.ContentLength != 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlayerBody)).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
				return
			}
		}
		s.playerUpdate(w, r, nil, func(q *player.Queue) error {
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
	if body != nil {
		// Routes that take a body require one: without it the struct stays at
		// its zero value and e.g. jump would silently go to the first track
		// or shuffle would silently turn off.
		if r.ContentLength == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body is required"})
			return
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPlayerBody)).Decode(body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
	}
	st, err := s.deps.Player.UpdateRadio(r.Context(), chi.URLParam(r, "session"), change, s.radioTracks)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, player.ErrTooLong) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.prewarmRadio(st)
	writeJSON(w, http.StatusOK, st)
}

// radioPrewarmAhead is how many upcoming external Radio tracks are resolved in
// advance, so they start without a wait.
const radioPrewarmAhead = 2

// prewarmRadio resolves Radio's next external tracks in the background, each
// once. Only Radio does: it lines tracks up itself, while a listener's queue
// is theirs to start.
func (s *Server) prewarmRadio(st player.State) {
	if !st.Radio || s.deps.ExternalStream == nil {
		return
	}
	for _, i := range st.UpNext[:min(radioPrewarmAhead, len(st.UpNext))] {
		var t struct {
			Artist         string `json:"artist"`
			Title          string `json:"title"`
			ExternalStream *struct {
				Source     string `json:"source"`
				ExternalID string `json:"externalId"`
			} `json:"externalStream"`
		}
		if json.Unmarshal(st.Entries[i].Track, &t) != nil || t.ExternalStream == nil {
			continue
		}
		if !s.prewarmed.claim(t.ExternalStream.Source + ":" + t.ExternalStream.ExternalID) {
			continue
		}
		resolver, source, id := s.deps.ExternalStream, t.ExternalStream.Source, t.ExternalStream.ExternalID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = resolver.ResolveHinted(ctx, source, id, t.Artist, t.Title)
		}()
	}
}

// prewarmSet remembers recently prewarmed tracks, bounded so a long session
// does not grow it without end; forgetting one costs a repeat resolve.
type prewarmSet struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (p *prewarmSet) claim(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen[key] {
		return false
	}
	if p.seen == nil || len(p.seen) >= 256 {
		p.seen = map[string]bool{}
	}
	p.seen[key] = true
	return true
}

// radioTracks projects recommendations into the same playable transport both
// clients already understand. Recommendation policy remains in recommend.
func (s *Server) radioTracks(ctx context.Context, seeds []player.RadioSeed) ([]json.RawMessage, error) {
	if s.deps.Recommend == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	input := make([]recommend.Seed, len(seeds))
	for i, v := range seeds {
		input[i] = recommend.Seed(v)
	}
	result := s.deps.Recommend.Radio(ctx, input)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []json.RawMessage
	for _, t := range result.Tracks {
		id := t.ExternalID
		row := map[string]any{"id": id, "title": t.Title, "artist": t.Artist, "album": t.Album, "durationMs": t.DurationMs, "albumId": "", "artistId": "", "coverArtId": t.CoverArtID, "trackNumber": 0, "discNumber": 0, "bitRate": 0, "suffix": "", "contentType": "", "recommendationOrigin": "radio", "reason": t.Reason, "isrc": t.ISRC, "mbid": t.MBID}
		if t.Source != "library" {
			row["id"] = trackref.EncodeExternalID(t.Source, id)
			row["externalStream"] = map[string]string{"source": t.Source, "externalId": id}
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}
