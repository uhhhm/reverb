// Package player owns the play queue, so every player on every platform plays
// one implementation of it rather than each keeping its own copy that drifts
// (ADR 0003).
//
// A player is thin. It tells the core what the listener did (play this list,
// queue that, skip, the track ended) and plays whatever the core then says is
// current; it never decides what comes next itself. What a player still owns is
// playback itself: loading audio, seeking, recovering a dropped stream, and
// deciding that a dead track should be skipped.
package player

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
)

// Origin says who put an entry in the queue.
type Origin string

const (
	// OriginListener is anything the listener chose.
	OriginListener Origin = "listener"
	// OriginRadio is a track a Radio session lined up. A track the listener
	// queues plays before these.
	OriginRadio Origin = "radio"
)

// Repeat is the repeat mode.
type Repeat string

const (
	RepeatOff Repeat = "off"
	RepeatAll Repeat = "all"
	RepeatOne Repeat = "one"
)

// UpNextLimit is how many upcoming positions State reports.
const UpNextLimit = 20

// MaxEntries bounds one queue. Playing a whole library is the largest real
// case, and the library browse endpoint stops at 20,000 songs.
const MaxEntries = 20000

var (
	// ErrTooLong is a change that would take the queue past MaxEntries.
	ErrTooLong = fmt.Errorf("the queue holds at most %d tracks", MaxEntries)
	// ErrNotATrack is a track that is not a JSON object.
	ErrNotATrack = errors.New("a track must be a JSON object")
	// ErrBadOrigin is an origin other than listener or radio.
	ErrBadOrigin = errors.New("origin must be listener or radio")
	// ErrBadRepeat is a repeat mode other than off, all or one.
	ErrBadRepeat = errors.New("repeat must be off, all or one")
)

// Entry is one queued track. The track is stored as the player sent it: the
// queue needs its position, not its contents.
type Entry struct {
	ID     string          `json:"id"`
	Origin Origin          `json:"origin"`
	Track  json.RawMessage `json:"track"`
}

// State is the queue as a player sees it.
type State struct {
	Radio   bool    `json:"radio"`
	Entries []Entry `json:"entries"`
	// Index is the current entry, or -1 with nothing to play.
	Index   int    `json:"index"`
	Shuffle bool   `json:"shuffle"`
	Repeat  Repeat `json:"repeat"`
	// UpNext is the positions that play after the current one, in the order
	// they will play — under shuffle, the rest of the shuffle order rather than
	// the tail of the queue. At most UpNextLimit.
	UpNext []int `json:"upNext"`
	// PlayID changes whenever the current entry has to start again from its
	// beginning: a new list, a skip, a jump, a track ending into the next one
	// or repeating. A player loads the current entry when it changes.
	PlayID int64 `json:"playId"`
	// Finished is set when the last track ended with nothing after it. The
	// current entry stays where it was, so pressing play replays it.
	Finished bool `json:"finished"`
	// Revision changes on every change to the queue.
	Revision int64 `json:"revision"`
}

// Queue is one play queue. It is not safe for concurrent use; Service
// serialises access.
type Queue struct {
	radio   *radioSession
	entries []Entry
	index   int
	shuffle bool
	repeat  Repeat
	// order is a permutation of positions under shuffle; pos points into it.
	order    []int
	pos      int
	playID   int64
	finished bool
	revision int64
	nextID   uint64
	rand     *rand.Rand
}

// NewQueue returns an empty queue. rng draws shuffle orders; nil seeds one.
func NewQueue(rng *rand.Rand) *Queue {
	if rng == nil {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	return &Queue{index: -1, pos: -1, repeat: RepeatOff, rand: rng}
}

// State reports the queue.
func (q *Queue) State() State {
	entries := make([]Entry, len(q.entries))
	copy(entries, q.entries)
	return State{
		Radio:    q.radio != nil,
		Entries:  entries,
		Index:    q.index,
		Shuffle:  q.shuffle,
		Repeat:   q.repeat,
		UpNext:   q.upcoming(UpNextLimit),
		PlayID:   q.playID,
		Finished: q.finished,
		Revision: q.revision,
	}
}

// Current is the current entry's id, or "" with nothing to play.
func (q *Queue) Current() string {
	if q.index < 0 || q.index >= len(q.entries) {
		return ""
	}
	return q.entries[q.index].ID
}

func (q *Queue) changed() { q.revision++ }

// restart marks the current entry as starting from its beginning.
func (q *Queue) restart() {
	q.playID++
	q.finished = false
}

func (q *Queue) newEntries(tracks []json.RawMessage, origin Origin) ([]Entry, error) {
	if origin == "" {
		origin = OriginListener
	}
	if origin != OriginListener && origin != OriginRadio {
		return nil, ErrBadOrigin
	}
	out := make([]Entry, len(tracks))
	for i, t := range tracks {
		if !isObject(t) {
			return nil, ErrNotATrack
		}
		q.nextID++
		out[i] = Entry{ID: "e" + strconv.FormatUint(q.nextID, 10), Origin: origin, Track: append(json.RawMessage(nil), t...)}
	}
	return out, nil
}

func isObject(raw json.RawMessage) bool {
	for _, c := range raw {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return json.Valid(raw)
		default:
			return false
		}
	}
	return false
}

// Play replaces the queue with tracks and starts at start, clamped into the
// list. An empty list empties the queue.
func (q *Queue) Play(tracks []json.RawMessage, start int, origin Origin) error {
	if len(tracks) > MaxEntries {
		return ErrTooLong
	}
	entries, err := q.newEntries(tracks, origin)
	if err != nil {
		return err
	}
	q.radio = nil
	q.entries = entries
	q.index = -1
	if len(entries) > 0 {
		q.index = min(max(start, 0), len(entries)-1)
	}
	q.rebuildShuffle()
	q.restart()
	q.changed()
	return nil
}

// Enqueue adds tracks after everything the listener queued. A listener's
// tracks go ahead of the first upcoming track a Radio session lined up, so
// what they chose plays before what Radio chose; Radio's go at the end.
func (q *Queue) Enqueue(tracks []json.RawMessage, origin Origin) error {
	at := len(q.entries)
	if origin != OriginRadio {
		for i := q.index + 1; i < len(q.entries); i++ {
			if q.entries[i].Origin == OriginRadio {
				at = i
				break
			}
		}
	}
	return q.insert(at, tracks, origin)
}

// insert puts tracks at a position, clamped into the queue, without
// interrupting the current one. Into an empty queue, the first becomes current
// but does not start: nothing plays until the listener presses play.
func (q *Queue) insert(at int, tracks []json.RawMessage, origin Origin) error {
	if len(q.entries)+len(tracks) > MaxEntries {
		return ErrTooLong
	}
	entries, err := q.newEntries(tracks, origin)
	if err != nil {
		return err
	}
	at = min(max(at, 0), len(q.entries))
	for n, e := range entries {
		q.insertOne(at+n, e)
	}
	if len(entries) > 0 {
		q.changed()
	}
	return nil
}

func (q *Queue) insertOne(i int, e Entry) {
	q.entries = append(q.entries, Entry{})
	copy(q.entries[i+1:], q.entries[i:])
	q.entries[i] = e
	if q.index == -1 {
		q.index = 0
	} else if i <= q.index {
		q.index++
	}
	if q.shuffle {
		for k, idx := range q.order {
			if idx >= i {
				q.order[k] = idx + 1
			}
		}
		q.order = append(q.order, i)
		q.pos = indexOf(q.order, q.index)
	}
}

// Remove takes the entries at the given positions out. Removing the current
// entry moves on to the one that would have played next, which starts from
// its beginning.
func (q *Queue) Remove(positions []int) {
	seen := map[int]bool{}
	var valid []int
	for _, p := range positions {
		if p >= 0 && p < len(q.entries) && !seen[p] {
			seen[p] = true
			valid = append(valid, p)
		}
	}
	// Highest first, so each removal leaves the rest where they were named.
	sort.Sort(sort.Reverse(sort.IntSlice(valid)))
	for _, p := range valid {
		q.removeOne(p)
	}
	if len(valid) > 0 {
		q.changed()
	}
}

func (q *Queue) removeOne(i int) {
	wasCurrent := i == q.index
	nextShuffled, hasNext := -1, false
	if q.shuffle {
		if q.pos+1 >= 0 && q.pos+1 < len(q.order) {
			nextShuffled, hasNext = q.order[q.pos+1], true
		} else if q.pos-1 >= 0 && q.pos-1 < len(q.order) {
			nextShuffled, hasNext = q.order[q.pos-1], true
		}
	}
	q.entries = append(q.entries[:i], q.entries[i+1:]...)
	if wasCurrent && q.shuffle && hasNext {
		q.index = nextShuffled
	}
	if i < q.index {
		q.index--
	}
	if q.index >= len(q.entries) {
		q.index = len(q.entries) - 1
	}
	if q.shuffle {
		order := q.order[:0]
		for _, idx := range q.order {
			switch {
			case idx == i:
			case idx > i:
				order = append(order, idx-1)
			default:
				order = append(order, idx)
			}
		}
		q.order = order
		q.pos = indexOf(q.order, q.index)
	}
	if wasCurrent {
		q.restart()
	}
}

// Move reorders one entry. The current entry stays current wherever it goes.
func (q *Queue) Move(from, to int) {
	n := len(q.entries)
	if from < 0 || from >= n || to < 0 || to >= n || from == to {
		return
	}
	e := q.entries[from]
	q.entries = append(q.entries[:from], q.entries[from+1:]...)
	q.entries = append(q.entries[:to], append([]Entry{e}, q.entries[to:]...)...)
	moved := func(i int) int {
		switch {
		case i == from:
			return to
		case from < i && i <= to:
			return i - 1
		case to <= i && i < from:
			return i + 1
		}
		return i
	}
	q.index = moved(q.index)
	for k, idx := range q.order {
		q.order[k] = moved(idx)
	}
	q.changed()
}

// Jump makes the entry at index current and starts it. Under shuffle the
// picked entry moves to right after the current place in the shuffle order,
// rather than the order jumping to wherever it sat, which would drop every
// entry in between from the cycle.
func (q *Queue) Jump(index int) {
	if index < 0 || index >= len(q.entries) {
		return
	}
	if q.shuffle {
		at := indexOf(q.order, index)
		if at >= 0 {
			q.order = append(q.order[:at], q.order[at+1:]...)
		}
		offset := 1
		if at >= 0 && at <= q.pos {
			offset = 0
		}
		insert := min(max(q.pos+offset, 0), len(q.order))
		q.order = append(q.order[:insert], append([]int{index}, q.order[insert:]...)...)
		q.pos = insert
	}
	q.index = index
	q.restart()
	q.changed()
}

// Next moves to the following entry. At the end with repeat off there is
// nothing to move to, and nothing changes.
func (q *Queue) Next() { q.advance(1, false) }

// Previous moves to the entry before, staying on the first. Restarting a
// track that is well under way is the player's call, since only it knows
// how far in it is.
func (q *Queue) Previous() { q.advance(-1, false) }

// Ended is the current entry playing to its end. With repeat one it starts
// again; otherwise the next entry starts, and at the end of the queue with
// nothing after it playback finishes.
func (q *Queue) Ended() {
	if len(q.entries) == 0 {
		return
	}
	if q.repeat == RepeatOne {
		q.restart()
		q.changed()
		return
	}
	q.advance(1, true)
}

func (q *Queue) advance(dir int, fromEnded bool) {
	if len(q.entries) == 0 {
		return
	}
	if q.shuffle {
		np := q.pos + dir
		if np >= len(q.order) {
			if q.repeat != RepeatAll {
				q.finish(fromEnded)
				return
			}
			np = 0
		}
		np = max(np, 0)
		q.pos = np
		q.index = q.order[np]
		q.restart()
		q.changed()
		return
	}
	ni := q.index + dir
	if ni >= len(q.entries) {
		if q.repeat != RepeatAll {
			q.finish(fromEnded)
			return
		}
		ni = 0
	}
	q.index = max(ni, 0)
	q.restart()
	q.changed()
}

func (q *Queue) finish(fromEnded bool) {
	if !fromEnded || q.finished {
		return
	}
	q.finished = true
	q.changed()
}

// SetShuffle turns shuffle on or off. Turning it on draws a new order that
// starts from the current entry.
func (q *Queue) SetShuffle(on bool) {
	if q.shuffle == on {
		return
	}
	q.shuffle = on
	q.rebuildShuffle()
	q.changed()
}

// SetRepeat sets the repeat mode.
func (q *Queue) SetRepeat(r Repeat) error {
	if r != RepeatOff && r != RepeatAll && r != RepeatOne {
		return ErrBadRepeat
	}
	if q.repeat != r {
		q.repeat = r
		q.changed()
	}
	return nil
}

// RadioEnded is a Radio session having stopped with tracks it lined up still
// queued. They stay, but as ordinary queue: from now on a track the listener
// queues goes after them, as it would with no Radio at all.
func (q *Queue) RadioEnded() {
	if q.radio != nil {
		q.radio = nil
		q.changed()
	}
	changed := false
	for i := range q.entries {
		if q.entries[i].Origin == OriginRadio {
			q.entries[i].Origin = OriginListener
			changed = true
		}
	}
	if changed {
		q.changed()
	}
}

// Clear empties the queue. Shuffle and repeat stay as they were.
func (q *Queue) Clear() {
	q.RadioEnded()
	if len(q.entries) == 0 && q.index == -1 {
		return
	}
	q.entries = nil
	q.index = -1
	q.rebuildShuffle()
	q.restart()
	q.changed()
}

func (q *Queue) rebuildShuffle() {
	if !q.shuffle {
		q.order, q.pos = nil, -1
		return
	}
	idxs := make([]int, len(q.entries))
	for i := range idxs {
		idxs[i] = i
	}
	q.rand.Shuffle(len(idxs), func(i, j int) { idxs[i], idxs[j] = idxs[j], idxs[i] })
	// The current entry leads the cycle, so turning shuffle on never replays
	// it or plays anything before it.
	if q.index >= 0 {
		if at := indexOf(idxs, q.index); at > 0 {
			idxs[0], idxs[at] = idxs[at], idxs[0]
		}
	}
	q.order, q.pos = idxs, 0
}

// upcoming is the next limit positions in play order.
func (q *Queue) upcoming(limit int) []int {
	out := []int{}
	if len(q.entries) == 0 || q.index < 0 {
		return out
	}
	if q.shuffle {
		for p := q.pos + 1; len(out) < limit; p++ {
			if p >= len(q.order) {
				if q.repeat != RepeatAll {
					break
				}
				p = -1
				continue
			}
			i := q.order[p]
			if i == q.index {
				break
			}
			out = append(out, i)
		}
		return out
	}
	for i := q.index + 1; len(out) < limit; i++ {
		if i >= len(q.entries) {
			if q.repeat != RepeatAll {
				break
			}
			i = -1
			continue
		}
		if i == q.index {
			break
		}
		out = append(out, i)
	}
	return out
}

func indexOf(xs []int, v int) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}
