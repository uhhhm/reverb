package player

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"

	"github.com/uhhhm/reverb/internal/recommend"
)

// TopicQueue is the event bus topic a queue change is published on.
const TopicQueue = "player.queue"

// TopicListen is the event bus topic a recorded listen is published on.
const TopicListen = "player.listen"

// ListenEvent says the core recorded a listen from a session's playback.
type ListenEvent struct {
	Session string `json:"session"`
}

// MaxSessions is how many queues the service keeps. Each open player has its
// own — a desktop window, a browser tab in server mode, the phone — and the
// least recently used is dropped past this, since a closed tab never says so.
const MaxSessions = 32

// ErrBadSession is a session id that is not 1-64 letters, digits, '-' or '_'.
var ErrBadSession = errors.New("session must be 1-64 letters, digits, '-' or '_'")

var sessionRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Event says a session's queue changed. It carries the revision rather than
// the queue, which can hold thousands of tracks: a listener that cares fetches
// the session's state.
type Event struct {
	Session  string `json:"session"`
	Revision int64  `json:"revision"`
}

// Service keeps one queue per player session and serialises changes to them.
type Service struct {
	mu       sync.Mutex
	sessions map[string]*session
	publish  func(Event)
	onListen func(Listen)
	now      func() time.Time
	// lookups counts Radio lookups running in the background.
	lookups sync.WaitGroup
}

type session struct {
	mu      sync.Mutex
	q       *Queue
	touched time.Time
}

// NewService returns a service that reports each change to publish, which may
// be nil.
func NewService(publish func(Event)) *Service {
	return &Service{sessions: map[string]*session{}, publish: publish, now: time.Now}
}

// OnListen has fn hear each listen the core decides on, outside any session
// lock. Set it before the service is used.
func (s *Service) OnListen(fn func(Listen)) { s.onListen = fn }

// State reports a session's queue; an unknown session is an empty queue.
func (s *Service) State(id string) (State, error) {
	if !sessionRe.MatchString(id) {
		return State{}, ErrBadSession
	}
	s.mu.Lock()
	if ses, ok := s.sessions[id]; ok {
		ses.touched = s.now()
		s.mu.Unlock()
		ses.mu.Lock()
		defer ses.mu.Unlock()
		return ses.q.State(), nil
	}
	s.mu.Unlock()
	return NewQueue(nil).State(), nil
}

// UpdateRadio applies change to a session's queue, creating it if need be, and
// returns the result. A change that alters the queue is published. fetch looks
// up recommendations when the session's Radio needs more. Each session is
// serialised on its own, so one player's change never waits on another's.
func (s *Service) UpdateRadio(ctx context.Context, id string, change func(*Queue) error, fetch RadioFetch) (State, error) {
	if !sessionRe.MatchString(id) {
		return State{}, ErrBadSession
	}
	s.mu.Lock()
	ses, ok := s.sessions[id]
	if !ok {
		s.evict()
		ses = &session{q: NewQueue(nil)}
		s.sessions[id] = ses
	}
	ses.touched = s.now()
	s.mu.Unlock()
	ses.mu.Lock()
	before := ses.q.revision
	err := change(ses.q)
	if err == nil {
		s.refill(ctx, id, ses, fetch)
	}
	ses.q.settle()
	listens := ses.q.listen.take()
	st := ses.q.State()
	ses.mu.Unlock()
	s.deliver(id, listens)
	if err != nil {
		return st, err
	}
	if st.Revision != before && s.publish != nil {
		s.publish(Event{Session: id, Revision: st.Revision})
	}
	return st, nil
}

// refill keeps a Radio session lined up; ses.mu is held. With nothing playing
// it waits for recommendations, since there is nothing else to play; otherwise
// it looks them up in the background, so no listener's request waits on a
// lookup. The player hears of the result in its next answer, and from the
// published change.
func (s *Service) refill(ctx context.Context, id string, ses *session, fetch RadioFetch) {
	q := ses.q
	// Bounded, since each lookup consumes its seeds.
	for range 4 {
		seeds, ok := q.radioNeeds()
		if !ok {
			return
		}
		if fetch == nil {
			q.RadioEnded()
			return
		}
		r := q.radio
		if q.index >= 0 {
			r.fetching = true
			s.lookups.Add(1)
			go s.lookUp(id, ses, r, seeds, fetch)
			return
		}
		rows, err := fetch(ctx, seeds)
		q.radioFetched(r, seeds, rows, err)
		if err != nil {
			return
		}
	}
}

// lookUp runs one background Radio lookup and applies it, unless the session
// has since ended or started another Radio.
func (s *Service) lookUp(id string, ses *session, r *radioSession, seeds []recommend.Seed, fetch RadioFetch) {
	defer s.lookups.Done()
	rows, err := fetch(context.Background(), seeds)
	ses.mu.Lock()
	q := ses.q
	before := q.revision
	r.fetching = false
	if q.radio == r {
		q.radioFetched(r, seeds, rows, err)
		if err == nil {
			s.refill(context.Background(), id, ses, fetch)
		}
	}
	q.settle()
	listens := q.listen.take()
	st := q.State()
	ses.mu.Unlock()
	s.deliver(id, listens)
	if st.Revision != before && s.publish != nil {
		s.publish(Event{Session: id, Revision: st.Revision})
	}
}

// deliver hands a session's listens to the listener, in the order decided.
func (s *Service) deliver(session string, listens []Listen) {
	if s.onListen == nil {
		return
	}
	for _, l := range listens {
		l.Session = session
		s.onListen(l)
	}
}

// Settle waits for background Radio lookups, including those they start.
func (s *Service) Settle() { s.lookups.Wait() }

// evict drops the least recently used session once the service is full.
func (s *Service) evict() {
	if len(s.sessions) < MaxSessions {
		return
	}
	var oldest string
	var at time.Time
	for id, ses := range s.sessions {
		if oldest == "" || ses.touched.Before(at) {
			oldest, at = id, ses.touched
		}
	}
	delete(s.sessions, oldest)
}
