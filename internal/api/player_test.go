package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uhhhm/reverb/internal/player"
)

// These cover, through the HTTP API, the queue behaviours the web player's
// own tests covered before the queue moved into the core.

type playerClient struct {
	t       *testing.T
	srv     *Server
	session string
}

func newPlayerClient(t *testing.T, publish func(player.Event)) *playerClient {
	t.Helper()
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts, Player: player.NewService(publish)})
	return &playerClient{t: t, srv: srv, session: "tab-1"}
}

func (c *playerClient) call(op string, body any) (int, player.State, string) {
	c.t.Helper()
	method, path := http.MethodPost, "/api/v1/player/"+c.session+"/"+op
	if op == "" {
		method, path = http.MethodGet, "/api/v1/player/"+c.session
	}
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			c.t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c.srv.Handler().ServeHTTP(rec, req)
	var st player.State
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	return rec.Code, st, rec.Body.String()
}

func (c *playerClient) do(op string, body any) player.State {
	c.t.Helper()
	code, st, raw := c.call(op, body)
	if code != http.StatusOK {
		c.t.Fatalf("%s: status %d: %s", op, code, raw)
	}
	return st
}

func tracks(ids ...string) []map[string]any {
	out := make([]map[string]any, len(ids))
	for i, id := range ids {
		out[i] = map[string]any{"id": id, "title": "T" + id, "artist": "Artist"}
	}
	return out
}

func (c *playerClient) play(start int, ids ...string) player.State {
	return c.do("play", map[string]any{"tracks": tracks(ids...), "start": start})
}

func ids(st player.State) []string {
	out := make([]string, len(st.Entries))
	for i, e := range st.Entries {
		var t struct{ ID string }
		_ = json.Unmarshal(e.Track, &t)
		out[i] = t.ID
	}
	return out
}

func current(st player.State) string {
	if st.Index < 0 {
		return ""
	}
	return ids(st)[st.Index]
}

func eq[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlayerPlaysAListFromAnIndex(t *testing.T) {
	c := newPlayerClient(t, nil)
	st := c.play(1, "1", "2", "3")
	if st.Index != 1 || current(st) != "2" || st.PlayID == 0 || !eq(st.UpNext, []int{2}) {
		t.Fatalf("state = %+v", st)
	}
	// The queue keeps the track exactly as the player sent it.
	var tr map[string]any
	_ = json.Unmarshal(st.Entries[0].Track, &tr)
	if tr["title"] != "T1" || st.Entries[0].Origin != player.OriginListener {
		t.Fatalf("entry = %s %s", st.Entries[0].Track, st.Entries[0].Origin)
	}
	if got := c.do("", nil); got.Revision != st.Revision || current(got) != "2" {
		t.Fatalf("GET = %+v", got)
	}
}

func TestPlayerNextAdvancesAndWrapsOnlyWithRepeatAll(t *testing.T) {
	c := newPlayerClient(t, nil)
	st := c.play(2, "1", "2", "3")
	after := c.do("next", nil)
	if after.Index != 2 || after.PlayID != st.PlayID {
		t.Fatalf("next at the end with repeat off moved: %+v", after)
	}
	c.do("repeat", map[string]string{"mode": "all"})
	if after = c.do("next", nil); after.Index != 0 || after.PlayID == st.PlayID {
		t.Fatalf("next with repeat all did not wrap: %+v", after)
	}
}

func TestPlayerPreviousGoesBackAndClampsAtTheStart(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(1, "1", "2", "3")
	if st := c.do("previous", nil); st.Index != 0 {
		t.Fatalf("index = %d", st.Index)
	}
	before := c.do("", nil)
	// On the first track, previous starts it again.
	if st := c.do("previous", nil); st.Index != 0 || st.PlayID == before.PlayID {
		t.Fatalf("previous at the start = %+v", st)
	}
}

func TestPlayerRepeatOneReplaysTheTrackOnEnd(t *testing.T) {
	c := newPlayerClient(t, nil)
	st := c.play(0, "1", "2", "3")
	c.do("repeat", map[string]string{"mode": "one"})
	after := c.do("ended", map[string]string{"entryId": st.Entries[0].ID})
	if after.Index != 0 || after.PlayID == st.PlayID || after.Repeat != player.RepeatOne {
		t.Fatalf("repeat one on end = %+v", after)
	}
}

func TestPlayerEndedAdvancesThenFinishes(t *testing.T) {
	c := newPlayerClient(t, nil)
	st := c.play(0, "1", "2")
	st = c.do("ended", map[string]string{"entryId": st.Entries[0].ID})
	if st.Index != 1 || st.Finished {
		t.Fatalf("after first end = %+v", st)
	}
	end := c.do("ended", map[string]string{"entryId": st.Entries[1].ID})
	if end.Index != 1 || !end.Finished || end.PlayID != st.PlayID {
		t.Fatalf("after the last end = %+v", end)
	}
	// Playing anything again clears it.
	if again := c.do("jump", map[string]int{"index": 0}); again.Finished {
		t.Fatalf("still finished after a jump: %+v", again)
	}
}

// An end reported for a track the listener already moved away from is stale
// and must not skip the one they chose.
func TestPlayerIgnoresAStaleEnd(t *testing.T) {
	c := newPlayerClient(t, nil)
	st := c.play(0, "1", "2", "3")
	first := st.Entries[0].ID
	st = c.do("jump", map[string]int{"index": 2})
	if after := c.do("ended", map[string]string{"entryId": first}); after.Index != 2 || after.PlayID != st.PlayID {
		t.Fatalf("a stale end moved the queue: %+v", after)
	}
	if after := c.do("next", map[string]string{"entryId": first}); after.Index != 2 {
		t.Fatalf("a stale skip moved the queue: %+v", after)
	}
}

func TestPlayerRepeatModes(t *testing.T) {
	c := newPlayerClient(t, nil)
	for _, m := range []string{"all", "one", "off"} {
		if st := c.do("repeat", map[string]string{"mode": m}); string(st.Repeat) != m {
			t.Fatalf("repeat = %s, want %s", st.Repeat, m)
		}
	}
	if code, _, _ := c.call("repeat", map[string]string{"mode": "twice"}); code != http.StatusBadRequest {
		t.Fatalf("an unknown repeat mode = %d", code)
	}
}

func TestPlayerShuffleVisitsEveryTrackOnce(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1", "2", "3", "4", "5")
	st := c.do("shuffle", map[string]bool{"on": true})
	if current(st) != "1" {
		t.Fatalf("turning shuffle on changed the current track: %s", current(st))
	}
	seen := map[string]bool{current(st): true}
	for i := 0; i < 4; i++ {
		st = c.do("next", nil)
		if seen[current(st)] {
			t.Fatalf("%s played twice in one shuffle cycle", current(st))
		}
		seen[current(st)] = true
	}
	if len(seen) != 5 || len(st.UpNext) != 0 {
		t.Fatalf("seen %v, up next %v", seen, st.UpNext)
	}
	if off := c.do("shuffle", map[string]bool{"on": false}); off.Shuffle {
		t.Fatal("shuffle stayed on")
	}
}

func TestPlayerEnqueueAndRemove(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1", "2", "3")
	st := c.do("enqueue", map[string]any{"tracks": tracks("4")})
	if !eq(ids(st), []string{"1", "2", "3", "4"}) {
		t.Fatalf("queue = %v", ids(st))
	}
	st = c.do("remove", map[string]any{"positions": []int{3}})
	if !eq(ids(st), []string{"1", "2", "3"}) || st.Index != 0 {
		t.Fatalf("queue = %v index %d", ids(st), st.Index)
	}
	st = c.do("remove", map[string]any{"positions": []int{0, 2}})
	if !eq(ids(st), []string{"2"}) || current(st) != "2" {
		t.Fatalf("removing the current track: %v index %d", ids(st), st.Index)
	}
	before := st.PlayID
	if st = c.do("remove", map[string]any{"positions": []int{0}}); st.Index != -1 || len(st.Entries) != 0 || st.PlayID == before {
		t.Fatalf("removing the last track = %+v", st)
	}
}

// A track queued into an empty queue becomes current but does not start.
func TestPlayerEnqueueIntoAnEmptyQueueWaitsForPlay(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1")
	c.do("clear", nil)
	before := c.do("", nil)
	st := c.do("enqueue", map[string]any{"tracks": tracks("new")})
	if current(st) != "new" || st.PlayID != before.PlayID {
		t.Fatalf("state = %+v", st)
	}
}

func TestPlayerMoveKeepsTheCurrentTrack(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1", "2", "3")
	st := c.do("move", map[string]int{"from": 0, "to": 2})
	if !eq(ids(st), []string{"2", "3", "1"}) || st.Index != 2 || current(st) != "1" {
		t.Fatalf("queue %v index %d", ids(st), st.Index)
	}
	// With the same track queued twice, the occurrence playing stays current.
	c.play(2, "1", "2", "1", "3")
	st = c.do("move", map[string]int{"from": 3, "to": 1})
	if st.Index != 3 || len(st.UpNext) != 0 {
		t.Fatalf("index %d up next %v", st.Index, st.UpNext)
	}
}

func TestPlayerJump(t *testing.T) {
	c := newPlayerClient(t, nil)
	st := c.play(0, "1", "2", "3")
	after := c.do("jump", map[string]int{"index": 2})
	if after.Index != 2 || current(after) != "3" || after.PlayID == st.PlayID {
		t.Fatalf("jump = %+v", after)
	}
	for _, bad := range []int{99, -1} {
		if st := c.do("jump", map[string]int{"index": bad}); st.Index != 2 || st.PlayID != after.PlayID {
			t.Fatalf("jump %d moved the queue: %+v", bad, st)
		}
	}
}

func TestPlayerJumpUnderShuffleKeepsWhatHasNotPlayed(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1", "2", "3")
	upcoming := c.do("shuffle", map[string]bool{"on": true}).UpNext
	st := c.do("jump", map[string]int{"index": upcoming[1]})
	if st.Index != upcoming[1] || !eq(st.UpNext, []int{upcoming[0]}) {
		t.Fatalf("jumped past %v: index %d up next %v", upcoming, st.Index, st.UpNext)
	}
	st = c.do("next", nil)
	if st.Index != upcoming[0] {
		t.Fatalf("next after the jump = %d, want %d", st.Index, upcoming[0])
	}
}

func TestPlayerUpNextFollowsTheShuffleOrder(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(2, "1", "2", "3")
	st := c.do("shuffle", map[string]bool{"on": true})
	if len(st.UpNext) != 2 || st.UpNext[0] == st.UpNext[1] || st.UpNext[0] == 2 || st.UpNext[1] == 2 {
		t.Fatalf("up next from the last row = %v", st.UpNext)
	}
	c.do("next", nil)
	if st = c.do("next", nil); len(st.UpNext) != 0 {
		t.Fatalf("up next on the last shuffled track = %v", st.UpNext)
	}
}

func TestPlayerUpNextWrapsWithRepeatAll(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(2, "1", "2", "3")
	if st := c.do("repeat", map[string]string{"mode": "all"}); !eq(st.UpNext, []int{0, 1}) {
		t.Fatalf("up next = %v", st.UpNext)
	}
}

func TestPlayerQueueingDoesNotReplayShuffleHistory(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1", "2", "3")
	c.do("shuffle", map[string]bool{"on": true})
	c.do("next", nil)
	st := c.do("enqueue", map[string]any{"tracks": tracks("4")})
	for _, i := range st.UpNext {
		if i == 0 {
			t.Fatalf("the played first track is up next again: %v", st.UpNext)
		}
	}
	if len(st.UpNext) != 2 {
		t.Fatalf("up next = %v", st.UpNext)
	}
}

// What the listener queues plays before what Radio lined up.
func TestPlayerListenerTracksGoBeforeRadioTracks(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "seed")
	st := c.do("enqueue", map[string]any{"tracks": tracks("r1", "r2"), "origin": "radio"})
	if st.Entries[1].Origin != player.OriginRadio {
		t.Fatalf("origin = %s", st.Entries[1].Origin)
	}
	c.do("enqueue", map[string]any{"tracks": tracks("m1")})
	st = c.do("enqueue", map[string]any{"tracks": tracks("m2")})
	if !eq(ids(st), []string{"seed", "m1", "m2", "r1", "r2"}) {
		t.Fatalf("queue = %v", ids(st))
	}
	st = c.do("enqueue", map[string]any{"tracks": tracks("r3"), "origin": "radio"})
	if !eq(ids(st), []string{"seed", "m1", "m2", "r1", "r2", "r3"}) {
		t.Fatalf("queue = %v", ids(st))
	}
	if code, _, _ := c.call("enqueue", map[string]any{"tracks": tracks("x"), "origin": "robot"}); code != http.StatusBadRequest {
		t.Fatalf("unknown origin = %d", code)
	}
}

// Once Radio has stopped, what it lined up is ordinary queue, and the
// listener's next track goes after it rather than jumping ahead.
func TestPlayerRadioEndedLeavesItsTracksAsOrdinaryQueue(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "seed")
	c.do("enqueue", map[string]any{"tracks": tracks("r1", "r2"), "origin": "radio"})
	st := c.do("radio-ended", nil)
	if st.Entries[1].Origin != player.OriginListener || st.Entries[2].Origin != player.OriginListener {
		t.Fatalf("origins after Radio ended: %s %s", st.Entries[1].Origin, st.Entries[2].Origin)
	}
	st = c.do("enqueue", map[string]any{"tracks": tracks("m1")})
	if !eq(ids(st), []string{"seed", "r1", "r2", "m1"}) {
		t.Fatalf("queue = %v", ids(st))
	}
}

func TestPlayerClear(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1", "2")
	c.do("shuffle", map[string]bool{"on": true})
	st := c.do("clear", nil)
	if st.Index != -1 || len(st.Entries) != 0 || len(st.UpNext) != 0 || !st.Shuffle {
		t.Fatalf("cleared = %+v", st)
	}
}

// Each player session has its own queue: a second browser tab does not
// take over the first one's.
func TestPlayerSessionsAreIndependent(t *testing.T) {
	c := newPlayerClient(t, nil)
	c.play(0, "1", "2")
	other := &playerClient{t: t, srv: c.srv, session: "tab-2"}
	if st := other.do("", nil); st.Index != -1 || len(st.Entries) != 0 {
		t.Fatalf("a new session saw another's queue: %+v", st)
	}
	other.play(0, "x")
	if st := c.do("", nil); !eq(ids(st), []string{"1", "2"}) {
		t.Fatalf("queue = %v", ids(st))
	}
	bad := &playerClient{t: t, srv: c.srv, session: "not%20valid"}
	if code, _, _ := bad.call("", nil); code != http.StatusBadRequest {
		t.Fatalf("bad session = %d", code)
	}
}

func TestPlayerRefusesWhatIsNotATrack(t *testing.T) {
	c := newPlayerClient(t, nil)
	if code, _, _ := c.call("play", map[string]any{"tracks": []any{"just a string"}}); code != http.StatusBadRequest {
		t.Fatalf("a string track = %d", code)
	}
	if code, _, _ := c.call("play", "nonsense"); code != http.StatusBadRequest {
		t.Fatalf("a malformed body = %d", code)
	}
}

func TestPlayerRequiresBodiesOnlyForOperationsThatNeedThem(t *testing.T) {
	c := newPlayerClient(t, nil)
	for _, op := range []string{"play", "enqueue", "remove", "move", "jump", "shuffle", "repeat"} {
		if code, _, raw := c.call(op, nil); code != http.StatusBadRequest {
			t.Errorf("%s without a body = %d: %s", op, code, raw)
		}
	}
	c.play(0, "1", "2")
	if code, st, raw := c.call("next", nil); code != http.StatusOK || st.Index != 1 {
		t.Fatalf("next without a body = %d %+v: %s", code, st, raw)
	}
}

func TestPlayerPublishesEachChange(t *testing.T) {
	var got []player.Event
	c := newPlayerClient(t, func(e player.Event) { got = append(got, e) })
	c.play(0, "1", "2")
	c.do("next", nil)
	c.do("next", nil) // at the end with repeat off: no change, nothing published
	if len(got) != 2 || got[0].Session != "tab-1" || got[1].Revision <= got[0].Revision {
		t.Fatalf("events = %s", fmt.Sprint(got))
	}
}

func TestPlayerUnavailableWithoutTheService(t *testing.T) {
	srv := NewServer(Deps{AllowedHosts: testAllowedHosts})
	c := &playerClient{t: t, srv: srv, session: "tab-1"}
	if code, _, _ := c.call("", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", code)
	}
}
