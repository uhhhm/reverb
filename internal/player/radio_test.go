package player_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/uhhhm/reverb/internal/player"
	"github.com/uhhhm/reverb/internal/recommend"
)

// One judgement of a play drives both what is recorded and how Radio steers:
// qualified, completed, or skipped (neither). Each case plays the seed track
// of a Radio session, leaves it, and asserts both outcomes.

type steer string

const (
	steeredUp   steer = "up"
	steeredDown steer = "down"
	steeredNone steer = "none"
)

// verdict is what a recorded play says of the listen.
type verdict struct{ qualified, completed bool }

// radioPool is what every Radio lookup answers. Its order is the baseline
// Radio queues from, so where the seed's artist S lands says which way the
// session steered.
func radioPool(context.Context, []recommend.Seed) ([]json.RawMessage, error) {
	track := func(id, artist string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Title %s","artist":%q,"album":"Album","durationMs":200000}`, id, id, artist))
	}
	return []json.RawMessage{track("x", "X"), track("s1", "S"), track("y", "Y"), track("s2", "S"), track("z", "Z")}, nil
}

// upNext is the artists Radio has lined up, in order.
func (l *listening) upNext() string {
	st := l.do(func(*player.Queue) error { return nil })
	var artists []string
	for _, i := range st.UpNext {
		var t struct{ Artist string }
		_ = json.Unmarshal(st.Entries[i].Track, &t)
		artists = append(artists, t.Artist)
	}
	return strings.Join(artists, " ")
}

// steered leaves the seed for the first track Radio lined up and reports which
// way Radio re-ranked what it has queued. From the pool's order (X S Y S Z),
// leaving with nothing to steer by keeps S's tracks where they were; a skip,
// judged as the seed is left, pushes them to the back; a completion, judged
// before the move, pulled them to the front already.
func (l *listening) steered() steer {
	l.t.Helper()
	l.next()
	switch got := l.upNext(); got {
	case "S Y S":
		return steeredNone
	case "X S Y":
		return steeredUp
	case "Y Z S":
		return steeredDown
	default:
		l.t.Fatalf("Radio queued %q after the seed was left", got)
		return ""
	}
}

func TestRadioSteersByTheSameJudgementThatRecordsTheListen(t *testing.T) {
	minutes := func(m int) int { return m * 60_000 }
	// seek is what a player reports for a jump: a sample at the target, marked seeking.
	seek := func(l *listening, to float64) {
		entry, playID := l.current()
		l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: to, Playing: true, Seeking: true})
	}

	cases := []struct {
		name       string
		durationMS int
		origin     string // the seed's recommendationOrigin; "" for an ordinary track
		play       func(l *listening)
		recorded   []verdict
		steer      steer
	}{
		{"a track too short to qualify, played to the end", 30_000, "radio", func(l *listening) { l.listen(0, 30_000) },
			[]verdict{{false, true}}, steeredUp},
		{"a track too short to qualify, left after half", 30_000, "radio", func(l *listening) { l.listen(0, 16_000) },
			[]verdict{{false, false}}, steeredDown},
		{"a track just over the floor, left after half", 31_000, "radio", func(l *listening) { l.listen(0, 16_000) },
			[]verdict{{true, false}}, steeredNone},
		{"just under half heard", 60_000, "radio", func(l *listening) { l.listen(0, 29_000) },
			[]verdict{{false, false}}, steeredDown},
		{"half heard", 60_000, "radio", func(l *listening) { l.listen(0, 30_000) },
			[]verdict{{true, false}}, steeredNone},
		{"a long track just under four minutes heard", minutes(10), "radio", func(l *listening) { l.listen(0, 239_000) },
			[]verdict{{false, false}}, steeredDown},
		{"a long track four minutes heard", minutes(10), "radio", func(l *listening) { l.listen(0, 240_000) },
			[]verdict{{true, false}}, steeredNone},
		{"a long track left after a qualifying listen", minutes(10), "radio", func(l *listening) { l.listen(0, 270_000) },
			[]verdict{{true, false}}, steeredNone},
		{"leaving early", minutes(3), "radio", func(l *listening) { l.listen(0, 40_000) },
			[]verdict{{false, false}}, steeredDown},
		{"a seek to the end is not a completion", 60_000, "radio", func(l *listening) {
			l.listen(0, 10_000)
			seek(l, 59_500)
		}, []verdict{{false, false}}, steeredDown},
		{"half heard, then a seek to the end, completes", 60_000, "radio", func(l *listening) {
			l.listen(0, 30_000)
			seek(l, 59_000)
			l.listen(59_000, 60_000)
		}, []verdict{{true, true}}, steeredUp},
		{"played through twice from the start", 60_000, "radio", func(l *listening) {
			l.listen(0, 60_000)
			l.listen(0, 60_000)
		}, []verdict{{true, true}, {true, true}}, steeredUp},
		{"restarted before qualifying, then heard enough to qualify", 60_000, "radio", func(l *listening) {
			l.listen(0, 20_000)
			l.do(func(q *player.Queue) error { q.Jump(0); return nil })
			if len(l.listens) != 0 {
				l.t.Errorf("restart recorded an unfinished attempt: %+v", l.listens)
			}
			l.listen(0, 15_000)
		}, []verdict{{true, false}}, steeredNone},
		{"restarted before qualifying, then left early", 60_000, "radio", func(l *listening) {
			l.listen(0, 10_000)
			l.do(func(q *player.Queue) error { q.Previous(); return nil })
			if len(l.listens) != 0 {
				l.t.Errorf("restart recorded an unfinished attempt: %+v", l.listens)
			}
			l.listen(0, 10_000)
		}, []verdict{{false, false}}, steeredDown},
		{"an ordinary track restarted after it qualified, then left early", 60_000, "", func(l *listening) {
			l.listen(0, 30_000)
			l.listen(0, 10_000)
		}, []verdict{{true, false}}, steeredDown},
		{"leaving before the length is known", 0, "radio", func(l *listening) {}, []verdict{{false, false}}, steeredDown},
		{"an ordinary track too short to qualify, played to the end", 20_000, "", func(l *listening) { l.listen(0, 20_000) },
			nil, steeredUp},
		{"an ordinary track left early", minutes(3), "", func(l *listening) { l.listen(0, 40_000) },
			nil, steeredDown},
		{"an ordinary track left after it qualified", minutes(5), "", func(l *listening) { l.listen(0, 200_000) },
			[]verdict{{true, false}}, steeredNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newListening(t)
			l.fetch = radioPool
			seed := fmt.Sprintf(`{"id":"seed","title":"Seed","artist":"S","album":"Album","durationMs":%d`, c.durationMS)
			if c.origin != "" {
				seed += fmt.Sprintf(`,"recommendationOrigin":%q`, c.origin)
			}
			l.do(func(q *player.Queue) error {
				return q.StartRadio(player.RadioStart{Lead: []json.RawMessage{json.RawMessage(seed + "}")}, Seeds: []recommend.Seed{{Artist: "S"}}})
			})
			if got := l.upNext(); got != "X S Y" {
				t.Fatalf("Radio started with %q lined up, want the pool's own order", got)
			}

			c.play(l)
			got := l.steered()

			var recorded []verdict
			for _, li := range l.listens {
				recorded = append(recorded, verdict{li.Qualified, li.Completed})
			}
			if fmt.Sprint(recorded) != fmt.Sprint(c.recorded) {
				t.Errorf("recorded %+v, want %+v", l.listens, c.recorded)
			}
			if got != c.steer {
				t.Errorf("Radio steered %s, want %s", got, c.steer)
			}
		})
	}
}

// A skip belongs to the session it was made in: the play a new Radio session
// replaces was left, but the new session starts unsteered.
func TestANewRadioSessionIsNotSteeredByThePlayItReplaced(t *testing.T) {
	l := newListening(t)
	l.fetch = radioPool
	start := func() {
		l.do(func(q *player.Queue) error {
			lead := json.RawMessage(`{"id":"seed","title":"Seed","artist":"S","album":"Album","durationMs":200000}`)
			return q.StartRadio(player.RadioStart{Lead: []json.RawMessage{lead}, Seeds: []recommend.Seed{{Artist: "S"}}})
		})
	}
	start()
	l.listen(0, 5_000)
	start()
	if got := l.upNext(); got != "X S Y" {
		t.Fatalf("the new session lined up %q, want the pool's own order", got)
	}
}
