package app

import (
	"context"
	"encoding/json"

	"github.com/uhhhm/reverb/internal/notinterested"
	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/store/db"
	"github.com/uhhhm/reverb/internal/tastehistory"
)

// tasteInputs reads the taste profile's inputs from the store: plays,
// playlist tracks and Not interested marks, which replicate so paired devices
// build the same profile (ADR 0001), and imported Last.fm history, which
// stays on the device holding the Last.fm link.
type tasteInputs struct {
	q       *db.Queries
	marks   *notinterested.Service
	history *tastehistory.Service
}

func (t tasteInputs) PlaysAfter(ctx context.Context, after int64, limit int) ([]recommend.TastePlay, error) {
	rows, err := t.q.ListTastePlaysAfter(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]recommend.TastePlay, len(rows))
	for i, r := range rows {
		out[i] = recommend.TastePlay{ID: r.ID, Seq: r.Seq, Artist: r.Artist, Title: r.Title, PlayedAt: r.PlayedAt, Completed: r.Completed}
	}
	return out, nil
}

func (t tasteInputs) PlayCount(ctx context.Context) (int64, error) { return t.q.CountPlays(ctx) }

func (t tasteInputs) PlayIDAt(ctx context.Context, seq int64) (string, error) {
	return t.q.TastePlayIDAt(ctx, seq)
}

func (t tasteInputs) Signals(ctx context.Context) ([]recommend.TasteSignal, error) {
	playlists, err := t.q.ListSyncedPlaylists(ctx)
	if err != nil {
		return nil, err
	}
	var out []recommend.TasteSignal
	for _, p := range playlists {
		var tracks []struct {
			Title  string `json:"title"`
			Artist string `json:"artist"`
		}
		// A tracklist that does not parse adds nothing; it is not a reason to
		// rank without the rest of the profile.
		if json.Unmarshal([]byte(p.TracksJson), &tracks) != nil {
			continue
		}
		for _, tr := range tracks {
			if tr.Title != "" && tr.Artist != "" {
				out = append(out, recommend.TasteSignal{Kind: recommend.SignalPlaylist, Artist: tr.Artist, Title: tr.Title, At: p.CreatedAt})
			}
		}
	}
	marks, err := t.marks.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range marks {
		sg := recommend.TasteSignal{Kind: recommend.SignalNotInterested, Artist: m.Artist, At: m.MarkedAt / 1000}
		if m.Kind == notinterested.KindTrack {
			sg.Title = m.Title
		}
		out = append(out, sg)
	}
	if t.history != nil {
		history, err := t.history.Signals(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, history...)
	}
	return out, nil
}
