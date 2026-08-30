package api

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/library/loudness"
)

// loudnessPageSize is how many tracks the backfill pulls per browse call.
const loudnessPageSize = 200

// backfillState is what the UI needs to render progress. It is a snapshot:
// callers get a copy, never the live struct.
type backfillState struct {
	Running   bool   `json:"running"`
	Total     int    `json:"total"`
	Done      int    `json:"done"`
	Skipped   int    `json:"skipped"`
	Failed    int    `json:"failed"`
	Error     string `json:"error,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
}

// loudnessBackfill measures every track in the library so normalization works
// on the first play rather than after it.
//
// Normal operation measures lazily, on demand — this is the opt-in bulk pass
// for someone who would rather spend the CPU up front. It is deliberately
// user-triggered: running ffmpeg over an entire library is not something to
// start behind the user's back.
type loudnessBackfill struct {
	mu     sync.Mutex
	state  backfillState
	cancel context.CancelFunc
}

func (b *loudnessBackfill) snapshot() backfillState {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *loudnessBackfill) stop() {
	b.mu.Lock()
	cancel := b.cancel
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) handleLoudnessBackfillStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loudness.snapshot())
}

// handleLoudnessBackfillStart kicks off the pass and returns immediately:
// measuring a large library takes far longer than any sensible request.
func (s *Server) handleLoudnessBackfillStart(w http.ResponseWriter, r *http.Request) {
	lib := s.library()
	browser, hasBrowse := lib.(songBrowser)
	paths, hasPaths := lib.(localTrackPath)
	if !hasBrowse || !hasPaths {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "measuring needs a library whose files are on this machine",
		})
		return
	}

	s.loudness.mu.Lock()
	if s.loudness.state.Running {
		s.loudness.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already measuring"})
		return
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	s.loudness.cancel = cancel
	s.loudness.state = backfillState{Running: true, StartedAt: time.Now().Unix()}
	s.loudness.mu.Unlock()

	go s.runLoudnessBackfill(ctx, browser, paths)
	writeJSON(w, http.StatusAccepted, s.loudness.snapshot())
}

// handleLoudnessBackfillCancel stops a pass in flight. Everything measured so
// far is kept — each measurement is cached on its own.
func (s *Server) handleLoudnessBackfillCancel(w http.ResponseWriter, r *http.Request) {
	s.loudness.stop()
	writeJSON(w, http.StatusOK, s.loudness.snapshot())
}

func (s *Server) runLoudnessBackfill(ctx context.Context, browser songBrowser, paths localTrackPath) {
	defer func() {
		s.loudness.mu.Lock()
		s.loudness.state.Running = false
		s.loudness.cancel = nil
		s.loudness.mu.Unlock()
	}()

	for offset := 0; ; offset += loudnessPageSize {
		if ctx.Err() != nil {
			return
		}
		tracks, err := browser.GetSongsBrowse(ctx, loudnessPageSize, offset)
		if err != nil {
			s.loudness.mu.Lock()
			s.loudness.state.Error = err.Error()
			s.loudness.mu.Unlock()
			return
		}
		if len(tracks) == 0 {
			return
		}
		s.loudness.mu.Lock()
		s.loudness.state.Total += len(tracks)
		s.loudness.mu.Unlock()

		for _, t := range tracks {
			if ctx.Err() != nil {
				return
			}
			s.measureOne(ctx, paths, t.ID)
		}
	}
}

// measureOne measures a single track, skipping one that already has a cached
// measurement — a re-run after a cancel then costs nothing for what was done.
func (s *Server) measureOne(ctx context.Context, paths localTrackPath, trackID string) {
	record := func(field *int) {
		s.loudness.mu.Lock()
		*field++
		s.loudness.mu.Unlock()
	}
	if s.deps.Loudness != nil {
		if _, err := s.deps.Loudness.GetTrackLoudness(ctx, trackID); err == nil {
			record(&s.loudness.state.Skipped)
			return
		}
	}
	path, ok := paths.LocalTrackPath(trackID)
	if !ok || path == "" {
		record(&s.loudness.state.Skipped)
		return
	}
	gain, err := loudness.Measure(ctx, "ffmpeg", path)
	if err != nil {
		record(&s.loudness.state.Failed)
		return
	}
	if s.deps.Loudness != nil {
		if err := s.storeTrackGain(ctx, trackID, gain); err != nil {
			log.Printf("WARNING: could not cache track loudness for %s: %v", trackID, err)
			record(&s.loudness.state.Failed)
			return
		}
	}
	record(&s.loudness.state.Done)
}
