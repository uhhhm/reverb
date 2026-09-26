package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/player"
	"github.com/uhhhm/reverb/internal/recommend"
)

// Failure cases: too few upcoming tracks, a third consecutive artist, listener
// entries moved by steering, skipped artists returning, and stale progress
// steering the next play. Exercise the same HTTP boundary both players use.
func TestPlayerRadioRefillsAndSteers(t *testing.T) {
	c := newPlayerClient(t, nil)
	rec := &fakeRecommendations{}
	for i, artist := range []string{"A", "A", "A", "B", "C", "D", "E", "F"} {
		rec.tracks.Tracks = append(rec.tracks.Tracks, core.ExternalResult{Source: "library", ExternalID: string(rune('a' + i)), Title: string(rune('a' + i)), Artist: artist, DurationMs: 100000})
	}
	c.srv.deps.Recommend = rec
	st := c.settled("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Lead", "artist": "A", "durationMs": 100000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Lead"}}})
	if len(st.UpNext) != 3 {
		t.Fatalf("up next = %v", st.UpNext)
	}
	artist := func(e player.Entry) string {
		var tr struct{ Artist string }
		_ = json.Unmarshal(e.Track, &tr)
		return tr.Artist
	}
	for n := 2; n < len(st.Entries); n++ {
		if a := artist(st.Entries[n]); a == artist(st.Entries[n-1]) && a == artist(st.Entries[n-2]) {
			t.Fatalf("three consecutive %s: %v", a, ids(st))
		}
	}
	st = c.settled("next", map[string]any{"entryId": st.Entries[st.Index].ID})
	// The lead was left at once: the first A skip, ranking A below the rest.
	// Hear the others through until an A comes up, and leave that too: the
	// second skip, which blocks A for the session.
	for guard := 0; artist(st.Entries[st.Index]) != "A"; guard++ {
		if guard == 8 {
			t.Fatalf("no second A came up: %v", ids(st))
		}
		for pos := 4000.0; pos <= 100000; pos += 4000 {
			st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": pos, "durationMs": 100000, "playing": true})
		}
		st = c.settled("ended", map[string]any{"entryId": st.Entries[st.Index].ID})
	}
	st = c.settled("next", map[string]string{"entryId": st.Entries[st.Index].ID})
	// Played out to the end, A never comes back.
	for guard := 0; st.Radio && guard < 10; guard++ {
		for _, i := range append([]int{st.Index}, st.UpNext...) {
			if artist(st.Entries[i]) == "A" {
				t.Fatalf("twice skipped artist came back: %v", ids(st))
			}
		}
		for pos := 4000.0; pos <= 100000; pos += 4000 {
			st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": pos, "durationMs": 100000, "playing": true})
		}
		st = c.settled("ended", map[string]any{"entryId": st.Entries[st.Index].ID})
	}
	if st.Radio {
		t.Fatalf("radio never ran dry: %v", ids(st))
	}
	c.settled("clear", nil)
	if st = c.settled("", nil); len(st.Entries) != 0 {
		t.Fatal("radio refilled after clear")
	}
}

func TestPlayerRadioListeningAndListenerEntries(t *testing.T) {
	c := newPlayerClient(t, nil)
	rec := &fakeRecommendations{}
	for i, a := range []string{"A", "B", "A", "C", "D", "E"} {
		rec.tracks.Tracks = append(rec.tracks.Tracks, core.ExternalResult{Source: "library", ExternalID: fmt.Sprint(i), Title: fmt.Sprint(i), Artist: a, DurationMs: 10000})
	}
	c.srv.deps.Recommend = rec
	start := func() player.State {
		return c.settled("radio", map[string]any{"lead": []map[string]any{{"id": "seed", "title": "Seed", "artist": "A", "durationMs": 10000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Seed"}}})
	}
	st := start()
	// A seek to the end, including a short seek, is not listening.
	for _, p := range []float64{3000, 6000, 9000} {
		st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": p, "durationMs": 10000, "playing": true, "seeking": true})
	}
	st = c.settled("next", nil)
	// The next A skipped twice across distinct entries must disappear, while
	// a listener's A stays exactly where the listener queued it.
	st = c.settled("enqueue", map[string]any{"tracks": []map[string]any{{"id": "mine", "title": "Mine", "artist": "A"}}})
	st = c.settled("next", nil)
	if current(st) != "mine" {
		t.Fatalf("listener entry moved: %v", ids(st))
	}
	for _, i := range st.UpNext {
		var tr struct{ Artist string }
		_ = json.Unmarshal(st.Entries[i].Track, &tr)
		if tr.Artist == "A" {
			t.Fatal("blocked A returned")
		}
	}
	st = start()
	for p := 1000; p <= 9000; p += 1000 {
		st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": p, "durationMs": 10000, "playing": true})
	}
	if got := ids(st); len(got) < 4 || got[1] != "0" || got[2] != "1" || got[3] != "2" {
		t.Fatalf("finished artist not preferred with run cap: %v", got)
	}
	st = c.settled("play", map[string]any{"tracks": tracks("chosen")})
	if st.Radio {
		t.Fatal("replacement kept radio")
	}
}

func radioRecs(artists ...string) *fakeRecommendations {
	rec := &fakeRecommendations{}
	for i, a := range artists {
		rec.tracks.Tracks = append(rec.tracks.Tracks, core.ExternalResult{Source: "library", ExternalID: fmt.Sprint("r", i), Title: fmt.Sprint("r", i), Artist: a, DurationMs: 10000})
	}
	return rec
}

func origins(st player.State) []player.Origin {
	out := make([]player.Origin, len(st.Entries))
	for i, e := range st.Entries {
		out[i] = e.Origin
	}
	return out
}

// Failure cases: an artist seed with no lead leaves nothing playing; the first
// recommendation is not marked as Radio's; the session refills from an empty
// queue forever instead of starting.
func TestPlayerRadioFromArtistStartsWithARecommendation(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.srv.deps.Recommend = radioRecs("B", "C", "D", "E", "F")
	st := c.settled("radio", map[string]any{"lead": []any{}, "seeds": []recommend.Seed{{Artist: "A"}}})
	if !st.Radio || st.Index != 0 || current(st) != "r0" || len(st.UpNext) != 3 {
		t.Fatalf("artist radio: radio=%v index=%d ids=%v upNext=%v", st.Radio, st.Index, ids(st), st.UpNext)
	}
	for _, o := range origins(st) {
		if o != player.OriginRadio {
			t.Fatalf("artist radio origins = %v", origins(st))
		}
	}
}

// Failure cases: steering pulls Radio's tracks out and only tops up to three
// ahead, so with the listener's own tracks queued Radio's lined-up tracks
// silently vanish; or they return in front of the listener's tracks.
func TestPlayerRadioSteeringKeepsRadioBehindListenerQueue(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.srv.deps.Recommend = radioRecs("B", "C", "D", "E", "F", "G")
	st := c.settled("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Lead", "artist": "A", "durationMs": 10000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Lead"}}})
	st = c.settled("enqueue", map[string]any{"tracks": tracks("m1", "m2", "m3")})
	radioAhead := func(st player.State) int {
		n := 0
		for _, i := range st.UpNext {
			if st.Entries[i].Origin == player.OriginRadio {
				n++
			}
		}
		return n
	}
	if radioAhead(st) != 3 {
		t.Fatalf("radio ahead before skip: %v %v", ids(st), origins(st))
	}
	// Skip the lead after two seconds: a skip that re-ranks Radio's tracks.
	st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": 1000, "durationMs": 10000, "playing": true})
	st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": 2000, "durationMs": 10000, "playing": true})
	st = c.settled("next", nil)
	if got := ids(st); current(st) != "m1" || got[2] != "m2" || got[3] != "m3" {
		t.Fatalf("listener queue moved: %v", got)
	}
	if radioAhead(st) != 3 {
		t.Fatalf("radio tracks lost on re-rank: %v %v", ids(st), origins(st))
	}
}

// Failure cases: Radio claims to run with nothing left to recommend, or drops
// what it had queued when it stops.
func TestPlayerRadioEndsKeepingItsQueue(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.srv.deps.Recommend = radioRecs("B", "C")
	st := c.settled("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Lead", "artist": "A", "durationMs": 10000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Lead"}}})
	if st.Radio {
		t.Fatal("radio still running with no more recommendations")
	}
	if got := ids(st); len(got) != 3 || got[1] != "r0" || got[2] != "r1" {
		t.Fatalf("ended radio dropped its queue: %v", got)
	}
	for _, o := range origins(st)[1:] {
		if o == player.OriginRadio {
			t.Fatalf("ended radio's tracks still marked radio: %v", origins(st))
		}
	}
}

// Failure case: a track finished, started over in place and left early is
// never judged again, so its artist escapes the two-skip block.
func TestPlayerRadioRestartAfterFinishIsJudgedAgain(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.srv.deps.Recommend = radioRecs("A", "B", "A", "C", "D", "E")
	st := c.settled("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Lead", "artist": "A", "durationMs": 10000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Lead"}}})
	progress := func(pos float64, seeking bool) {
		st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": pos, "durationMs": 10000, "playing": true, "seeking": seeking})
	}
	for p := 1000.0; p <= 9500; p += 1000 {
		progress(p, false)
	}
	progress(0, true)
	progress(1000, false)
	st = c.settled("next", nil) // the restarted lead, left early: a skip of A
	st = c.settled("next", nil) // an A recommendation, left at once: the second
	for _, i := range st.UpNext {
		var tr struct{ Artist string }
		_ = json.Unmarshal(st.Entries[i].Track, &tr)
		if tr.Artist == "A" {
			t.Fatalf("A survived two skips: %v", ids(st))
		}
	}
}

// A whole Radio session as the iPhone app drives it: tracks whose length only
// the player knows (the phone's folder library reads none), a finish told
// apart from a skip by reported listening alone, the listener's own track
// queued mid-session, two skips of one artist, then something else played.
// Failure cases: a finish judged a skip for want of a duration; three in a row
// by one artist; the listener's track moved; a twice-skipped artist queued
// again by a later refill; Radio refilling after the listener moved on.
// REVERB_RADIO_E2E_REPORT names a file for the session's steps.
func TestPlayerRadioSessionAsAPlayerDrivesIt(t *testing.T) {
	c := newPlayerClient(t, nil)
	rec := &fakeRecommendations{}
	for i, title := range []string{"Bravo One", "Alpha Two", "Charlie One", "Alpha Three", "Delta One", "Bravo Two", "Echo One", "Charlie Two", "Alpha Four", "Delta Two", "Echo Two", "Bravo Three", "Charlie Three"} {
		rec.tracks.Tracks = append(rec.tracks.Tracks, core.ExternalResult{Source: "library", ExternalID: fmt.Sprint("t", i), Title: title, Artist: strings.Fields(title)[0]})
	}
	c.srv.deps.Recommend = rec
	var steps []map[string]any
	var st player.State
	titleAt := func(i int) string {
		var tr struct{ Title string }
		_ = json.Unmarshal(st.Entries[i].Track, &tr)
		return tr.Title
	}
	artistAt := func(i int) string { return strings.Fields(titleAt(i))[0] }
	do := func(op string, body any) {
		t.Helper()
		st = c.settled(op, body)
		up := []string{}
		for _, i := range st.UpNext {
			up = append(up, titleAt(i))
		}
		cur := ""
		if st.Index >= 0 {
			cur = titleAt(st.Index)
		}
		steps = append(steps, map[string]any{"op": op, "radio": st.Radio, "current": cur, "upNext": up})
	}
	listen := func(toMs float64) {
		t.Helper()
		for pos := 1000.0; pos <= toMs; pos += 1000 {
			do("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": pos, "durationMs": 30000, "playing": true})
		}
	}
	noThreeInARow := func() {
		t.Helper()
		lined := append([]int{st.Index}, st.UpNext...)
		for i := 2; i < len(lined); i++ {
			if a := artistAt(lined[i]); a == artistAt(lined[i-1]) && a == artistAt(lined[i-2]) && st.Entries[lined[i]].Origin == player.OriginRadio {
				t.Fatalf("three in a row by %s: %v", a, steps[len(steps)-1])
			}
		}
	}

	do("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Alpha One", "artist": "Alpha"}}, "seeds": []recommend.Seed{{Artist: "Alpha", Title: "Alpha One"}}})
	if !st.Radio || titleAt(st.Index) != "Alpha One" || len(st.UpNext) != 3 {
		t.Fatalf("radio did not start: %v", steps)
	}
	noThreeInARow()

	// The lead plays through: a finish, which pulls Alpha forward.
	listen(30000)
	do("ended", map[string]any{"entryId": st.Entries[st.Index].ID})
	if !st.Radio || titleAt(st.Index) != "Alpha Two" {
		t.Fatalf("a finish did not pull Alpha forward: %v", steps[len(steps)-1])
	}
	noThreeInARow()

	// The listener queues their own track; it goes ahead of Radio's.
	do("enqueue", map[string]any{"tracks": []map[string]any{{"id": "mine", "title": "Echo Mine", "artist": "Echo"}}})
	if titleAt(st.UpNext[0]) != "Echo Mine" {
		t.Fatalf("listener track not next: %v", steps[len(steps)-1])
	}

	// Alpha Two is left after two seconds: a skip.
	listen(2000)
	do("next", map[string]any{"entryId": st.Entries[st.Index].ID})
	if titleAt(st.Index) != "Echo Mine" {
		t.Fatalf("steering moved the listener's track: %v", steps[len(steps)-1])
	}
	// Hear the listener's track out, then jump to the next Alpha and skip it.
	listen(30000)
	do("ended", map[string]any{"entryId": st.Entries[st.Index].ID})
	jumped := false
	for _, i := range st.UpNext {
		if artistAt(i) == "Alpha" {
			do("jump", map[string]int{"index": i})
			jumped = true
			break
		}
	}
	if !jumped {
		t.Fatalf("no second Alpha to skip: %v", steps[len(steps)-1])
	}
	listen(2000)
	do("next", map[string]any{"entryId": st.Entries[st.Index].ID})
	noThreeInARow()
	// Refill until the recommendations run dry: Alpha never comes back.
	for guard := 0; st.Radio && guard < 20; guard++ {
		for _, i := range st.UpNext {
			if artistAt(i) == "Alpha" {
				t.Fatalf("Alpha queued after two skips: %v", steps[len(steps)-1])
			}
		}
		listen(30000)
		do("ended", map[string]any{"entryId": st.Entries[st.Index].ID})
		noThreeInARow()
	}

	do("play", map[string]any{"tracks": tracks("chosen")})
	if st.Radio || len(st.Entries) != 1 {
		t.Fatalf("radio outlived playing something else: %v", steps[len(steps)-1])
	}
	if path := os.Getenv("REVERB_RADIO_E2E_REPORT"); path != "" {
		b, _ := json.MarshalIndent(map[string]any{"steps": steps}, "", "  ")
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// settled sends an operation and answers the queue once any Radio lookup it
// started in the background has landed, as a player sees it a moment later.
func (c *playerClient) settled(op string, body any) player.State {
	c.t.Helper()
	st := c.do(op, body)
	c.srv.deps.Player.Settle()
	if op == "" {
		return st
	}
	return c.do("", nil)
}

type slowRecommendations struct {
	fakeRecommendations
	release chan struct{}
}

func (s *slowRecommendations) Radio(ctx context.Context, seeds []recommend.Seed) recommend.TrackResult {
	<-s.release
	return s.fakeRecommendations.Radio(ctx, seeds)
}

// Failure cases: starting Radio from a track waits for recommendations before
// the lead plays; a listener's next or progress waits behind a lookup.
func TestPlayerRadioLooksUpInTheBackground(t *testing.T) {
	c := newPlayerClient(t, nil)
	rec := &slowRecommendations{release: make(chan struct{})}
	rec.tracks = radioRecs("B", "C", "D", "E", "F").tracks
	c.srv.deps.Recommend = rec
	done := make(chan player.State, 1)
	go func() {
		done <- c.do("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Lead", "artist": "A", "durationMs": 10000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Lead"}}})
	}()
	var st player.State
	select {
	case st = <-done:
	case <-time.After(5 * time.Second):
		close(rec.release)
		t.Fatal("starting radio waited for the lookup")
	}
	if !st.Radio || current(st) != "lead" {
		t.Fatalf("lead not playing at once: %v", ids(st))
	}
	progressed := make(chan struct{})
	go func() {
		c.do("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": 1000, "durationMs": 10000, "playing": true})
		close(progressed)
	}()
	select {
	case <-progressed:
	case <-time.After(5 * time.Second):
		close(rec.release)
		t.Fatal("progress waited behind the lookup")
	}
	close(rec.release)
	c.srv.deps.Player.Settle()
	if st = c.do("", nil); len(st.UpNext) != 3 {
		t.Fatalf("lookup never landed: %v", ids(st))
	}
}

// Failure case: starting the current track over (a jump to itself) is judged
// a skip, so restarting twice blocks the artist the listener is replaying.
func TestPlayerRadioRestartIsNotASkip(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.srv.deps.Recommend = radioRecs("B", "A", "C", "A", "D", "E")
	st := c.settled("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Lead", "artist": "A", "durationMs": 10000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Lead"}}})
	for range 2 {
		st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": 2000, "durationMs": 10000, "playing": true})
		st = c.settled("jump", map[string]int{"index": st.Index})
	}
	st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": 1000, "durationMs": 10000, "playing": true})
	sawA := false
	for _, i := range st.UpNext {
		var tr struct{ Artist string }
		_ = json.Unmarshal(st.Entries[i].Track, &tr)
		sawA = sawA || tr.Artist == "A"
	}
	if !sawA {
		t.Fatalf("restarts blocked A: %v", ids(st))
	}
}

type prewarmRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (p *prewarmRecorder) ResolveHinted(_ context.Context, _, id, _, _ string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = append(p.ids, id)
	return "http://audio.invalid/" + id, nil
}
func (p *prewarmRecorder) Invalidate(string, string) {}
func (p *prewarmRecorder) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.ids...)
}

// Failure cases: Radio's next external tracks start cold, because no player
// resolves them ahead; every upcoming track is resolved, or the same one
// again on each answer; an ordinary queue is resolved ahead as well.
func TestPlayerRadioPrewarmsTheNextTwoExternalTracks(t *testing.T) {
	c := newPlayerClient(t, nil)
	rec := &fakeRecommendations{}
	for i, a := range []string{"B", "C", "D", "E"} {
		rec.tracks.Tracks = append(rec.tracks.Tracks, core.ExternalResult{Source: "deezer", ExternalID: fmt.Sprint("e", i), Title: fmt.Sprint("e", i), Artist: a, DurationMs: 10000})
	}
	c.srv.deps.Recommend = rec
	warm := &prewarmRecorder{}
	c.srv.deps.ExternalStream = warm
	st := c.settled("radio", map[string]any{"lead": []map[string]any{{"id": "lead", "title": "Lead", "artist": "A", "durationMs": 10000}}, "seeds": []recommend.Seed{{Artist: "A", Title: "Lead"}}})
	for range 3 {
		st = c.settled("progress", map[string]any{"entryId": st.Entries[st.Index].ID, "playId": st.PlayID, "positionMs": 1000, "durationMs": 10000, "playing": true})
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(warm.seen()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := warm.seen(); len(got) != 2 || got[0] == got[1] || (got[0] != "e0" && got[0] != "e1") || (got[1] != "e0" && got[1] != "e1") {
		t.Fatalf("prewarmed %v, want e0 and e1 once each", got)
	}
	c.settled("play", map[string]any{"tracks": []map[string]any{
		{"id": "x1", "title": "X1", "artist": "X", "externalStream": map[string]string{"source": "deezer", "externalId": "x1"}},
		{"id": "x2", "title": "X2", "artist": "X", "externalStream": map[string]string{"source": "deezer", "externalId": "x2"}},
	}})
	time.Sleep(100 * time.Millisecond)
	if got := warm.seen(); len(got) != 2 {
		t.Fatalf("an ordinary queue was prewarmed: %v", got)
	}
}
