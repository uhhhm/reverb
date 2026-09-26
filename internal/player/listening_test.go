package player_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/uhhhm/reverb/internal/player"
)

// A player reports playback samples of the current play; the core decides
// what was listened to. These are the ways that judgement can go wrong.

func song(id string, durationMS int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Title %s","artist":"Artist","album":"Album","isrc":"ISRC-%s","durationMs":%d}`, id, id, id, durationMS))
}

func recommended(id string, durationMS int, origin string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"id":%q,"title":"Title %s","artist":"Artist","album":"Album","durationMs":%d,"recommendationOrigin":%q}`, id, id, durationMS, origin))
}

// listening is one player session with every listen the core decided on.
type listening struct {
	t       *testing.T
	svc     *player.Service
	session string
	listens []player.Listen
}

func newListening(t *testing.T) *listening {
	l := &listening{t: t, svc: player.NewService(nil), session: "tab-1"}
	l.svc.OnListen(func(li player.Listen) {
		// Listens are delivered outside the session lock: reading the
		// session from here must not deadlock.
		if _, err := l.svc.State(li.Session); err != nil {
			t.Errorf("state from listener: %v", err)
		}
		l.listens = append(l.listens, li)
	})
	return l
}

func (l *listening) do(change func(*player.Queue) error) player.State {
	l.t.Helper()
	st, err := l.svc.Update(l.session, change)
	if err != nil {
		l.t.Fatal(err)
	}
	return st
}

func (l *listening) play(tracks ...json.RawMessage) player.State {
	return l.do(func(q *player.Queue) error { return q.Play(tracks, 0, player.OriginListener) })
}

func (l *listening) sample(p player.Progress) {
	l.t.Helper()
	l.do(func(q *player.Queue) error { q.Progress(p); return nil })
}

// listen plays the current entry from fromMS to toMS in one-second samples,
// as a player reporting once a second does.
func (l *listening) listen(fromMS, toMS float64) {
	l.t.Helper()
	st := l.do(func(*player.Queue) error { return nil })
	cur := st.Entries[st.Index].ID
	for at := fromMS; ; at += 1000 {
		at = min(at, toMS)
		l.sample(player.Progress{EntryID: cur, PlayID: st.PlayID, PositionMS: at, Playing: true})
		if at == toMS {
			return
		}
	}
}

func (l *listening) current() (string, int64) {
	st := l.do(func(*player.Queue) error { return nil })
	return st.Entries[st.Index].ID, st.PlayID
}

func (l *listening) next() { l.do(func(q *player.Queue) error { q.Next(); return nil }) }

func TestAnOrdinaryPlayIsRecordedOnceWhenHalfIsHeard(t *testing.T) {
	l := newListening(t)
	l.play(song("a", 60_000), song("b", 60_000))
	l.listen(0, 29_000)
	if len(l.listens) != 0 {
		t.Fatalf("recorded before half was heard: %+v", l.listens)
	}
	l.listen(29_000, 31_000)
	if len(l.listens) != 1 {
		t.Fatalf("listens = %+v, want one at half", l.listens)
	}
	got := l.listens[0]
	if got.TrackID != "a" || got.Title != "Title a" || got.Artist != "Artist" || got.Album != "Album" || got.ISRC != "ISRC-a" || got.DurationMS != 60_000 {
		t.Fatalf("listen describes %+v", got)
	}
	if !got.Qualified || got.Completed || got.Origin != "" || got.MsPlayed < 30_000 || got.MsPlayed > 31_000 {
		t.Fatalf("listen = %+v, want qualified, not completed, ~31s heard", got)
	}
	// Hearing the rest and moving on records nothing more.
	l.listen(31_000, 60_000)
	l.next()
	if len(l.listens) != 1 {
		t.Fatalf("listened track recorded again: %+v", l.listens)
	}
}

func TestFourMinutesQualifiesALongTrack(t *testing.T) {
	l := newListening(t)
	l.play(song("long", 20*60_000))
	l.listen(0, 239_000)
	if len(l.listens) != 0 {
		t.Fatal("qualified before four minutes")
	}
	l.listen(239_000, 240_000)
	if len(l.listens) != 1 {
		t.Fatalf("listens = %d after four minutes of a twenty-minute track", len(l.listens))
	}
}

func TestOnlyHeardTimeCounts(t *testing.T) {
	cases := map[string]func(l *listening, entry string, playID int64){
		"a seek forward is not heard": func(l *listening, entry string, playID int64) {
			l.listen(0, 10_000)
			l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: 50_000, Playing: true, Seeking: true})
			l.listen(50_000, 55_000)
		},
		"a jump forward without a seek is not heard": func(l *listening, entry string, playID int64) {
			l.listen(0, 10_000)
			l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: 50_000, Playing: true})
			l.listen(50_000, 55_000)
		},
		"a seek back is not heard twice": func(l *listening, entry string, playID int64) {
			l.listen(0, 25_000)
			l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: 5_000, Playing: true, Seeking: true})
			l.listen(5_000, 10_000)
		},
		"paused time is not heard": func(l *listening, entry string, playID int64) {
			l.listen(0, 10_000)
			for at := 10_000.0; at <= 60_000; at += 1000 {
				l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: at})
			}
		},
		"a sample for another play is ignored": func(l *listening, entry string, playID int64) {
			l.listen(0, 10_000)
			for at := 11_000.0; at <= 60_000; at += 1000 {
				l.sample(player.Progress{EntryID: entry, PlayID: playID - 1, PositionMS: at, Playing: true})
				l.sample(player.Progress{EntryID: "e-gone", PlayID: playID, PositionMS: at, Playing: true})
			}
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			l := newListening(t)
			l.play(song("a", 100_000), song("b", 100_000))
			entry, playID := l.current()
			run(l, entry, playID)
			l.next()
			if len(l.listens) != 0 {
				t.Fatalf("recorded %+v from under half a track heard", l.listens)
			}
		})
	}
}

func TestAShortTrackIsNeverAListen(t *testing.T) {
	l := newListening(t)
	l.play(song("jingle", 30_000), song("b", 60_000))
	l.listen(0, 30_000)
	l.next()
	if len(l.listens) != 0 {
		t.Fatalf("a 30s track was recorded: %+v", l.listens)
	}
}

func TestRepeatOneRecordsEachTimeThrough(t *testing.T) {
	l := newListening(t)
	l.play(song("loop", 60_000))
	l.do(func(q *player.Queue) error { return q.SetRepeat(player.RepeatOne) })
	for range 2 {
		l.listen(0, 60_000)
		entry, _ := l.current()
		l.do(func(q *player.Queue) error {
			if q.Current() == entry {
				q.Ended()
			}
			return nil
		})
	}
	if len(l.listens) != 2 || !l.listens[0].Qualified || !l.listens[1].Qualified {
		t.Fatalf("listens = %+v over two times through, want 2", l.listens)
	}
}

func TestTheDurationComesFromTheTrackUntilPlaybackKnowsIt(t *testing.T) {
	l := newListening(t)
	l.play(song("a", 60_000))
	entry, playID := l.current()
	for at := 1000.0; at <= 31_000; at += 1000 {
		l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: at, Playing: true}) // DurationMS unknown
	}
	if len(l.listens) != 1 || l.listens[0].DurationMS != 60_000 {
		t.Fatalf("listens = %+v, want one with the track's 60s", l.listens)
	}
}

func TestARecommendationSkippedEarlyIsAnUnqualifiedAttempt(t *testing.T) {
	l := newListening(t)
	l.play(recommended("rec", 120_000, "mix"), song("b", 60_000))
	l.listen(0, 10_000)
	if len(l.listens) != 0 {
		t.Fatal("an attempt was recorded while still playing")
	}
	l.next()
	if len(l.listens) != 1 {
		t.Fatalf("listens = %+v, want the skipped attempt", l.listens)
	}
	got := l.listens[0]
	if got.Origin != "mix" || got.Qualified || got.Completed || got.MsPlayed != 10_000 || got.Run == "" {
		t.Fatalf("attempt = %+v, want mix, unqualified, not completed, 10s, in a run", got)
	}
}

func TestARadioEntryIsARadioRecommendation(t *testing.T) {
	// A player's own track JSON need not name an origin: Radio's entries are
	// Radio's recommendations all the same.
	l := newListening(t)
	l.play(song("lead", 60_000))
	l.do(func(q *player.Queue) error {
		return q.Enqueue([]json.RawMessage{song("r1", 90_000)}, player.OriginRadio)
	})
	l.next()
	l.listen(0, 50_000)
	l.do(func(q *player.Queue) error { q.Clear(); return nil })
	if len(l.listens) != 1 || l.listens[0].Origin != "radio" || !l.listens[0].Qualified {
		t.Fatalf("listens = %+v, want one qualified radio attempt", l.listens)
	}
}

func TestARecommendationHeardToTheEndIsACompletedListenOnce(t *testing.T) {
	l := newListening(t)
	l.play(recommended("rec", 60_000, "radio"), song("b", 60_000))
	l.listen(0, 59_000)
	if len(l.listens) != 1 {
		t.Fatalf("listens = %+v, want the attempt as it reached the end", l.listens)
	}
	if got := l.listens[0]; !got.Qualified || !got.Completed || got.Origin != "radio" {
		t.Fatalf("attempt = %+v", got)
	}
	l.listen(59_000, 60_000)
	l.next()
	if len(l.listens) != 1 {
		t.Fatalf("completed attempt recorded again: %+v", l.listens)
	}
}

func TestARecommendationOfUnknownLengthIsNeverCompleted(t *testing.T) {
	l := newListening(t)
	l.play(recommended("rec", 0, "shelf"), song("b", 60_000))
	entry, playID := l.current()
	l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: 0, Playing: true})
	l.sample(player.Progress{EntryID: entry, PlayID: playID, PositionMS: 500, Playing: true})
	if len(l.listens) != 0 {
		t.Fatalf("recorded on load: %+v", l.listens)
	}
	l.next()
	if len(l.listens) != 1 || l.listens[0].Completed || l.listens[0].Qualified {
		t.Fatalf("listens = %+v, want one incomplete, unqualified attempt", l.listens)
	}
}

func TestPlaysInOneRunShareARunID(t *testing.T) {
	l := newListening(t)
	l.play(song("a", 60_000), song("b", 60_000))
	l.listen(0, 31_000)
	l.next()
	l.listen(0, 31_000)
	// The queue plays out: the run is over.
	entry, _ := l.current()
	l.do(func(q *player.Queue) error {
		if q.Current() == entry {
			q.Ended()
		}
		return nil
	})
	l.do(func(q *player.Queue) error { q.Jump(0); return nil })
	l.listen(0, 31_000)
	// Another player listening meanwhile is a run of its own.
	l.session = "phone"
	l.play(song("c", 60_000))
	l.listen(0, 31_000)

	if len(l.listens) != 4 {
		t.Fatalf("listens = %+v, want 4", l.listens)
	}
	first, second, replay, phone := l.listens[0], l.listens[1], l.listens[2], l.listens[3]
	if first.Run == "" || first.Run != second.Run {
		t.Fatalf("consecutive plays in runs %q and %q", first.Run, second.Run)
	}
	if replay.Run == first.Run {
		t.Fatal("playing again after the queue finished continued the old run")
	}
	if phone.Run == replay.Run || phone.Session != "phone" || replay.Session != "tab-1" {
		t.Fatalf("sessions %q/%q share run %q", replay.Session, phone.Session, phone.Run)
	}
}

func TestLeavingAPlayEndsItsAttempt(t *testing.T) {
	leave := map[string]func(*player.Queue){
		"clear":          func(q *player.Queue) { q.Clear() },
		"remove current": func(q *player.Queue) { q.Remove([]int{0}) },
		"play a new list": func(q *player.Queue) {
			_ = q.Play([]json.RawMessage{song("z", 60_000)}, 0, player.OriginListener)
		},
		"jump": func(q *player.Queue) { q.Jump(1) },
	}
	for name, move := range leave {
		t.Run(name, func(t *testing.T) {
			l := newListening(t)
			l.play(recommended("rec", 120_000, "similarTracks"), song("b", 60_000))
			l.listen(0, 5_000)
			l.do(func(q *player.Queue) error { move(q); return nil })
			if len(l.listens) != 1 || l.listens[0].Origin != "similarTracks" || l.listens[0].MsPlayed != 5_000 {
				t.Fatalf("listens = %+v, want the 5s attempt", l.listens)
			}
		})
	}
}
