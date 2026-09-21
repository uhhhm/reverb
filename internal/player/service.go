package player

import (
	"errors"
	"regexp"
	"sync"
	"time"
)

// TopicQueue is the event bus topic a queue change is published on.
const TopicQueue = "player.queue"

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
	now      func() time.Time
}

type session struct {
	q       *Queue
	touched time.Time
}

// NewService returns a service that reports each change to publish, which may
// be nil.
func NewService(publish func(Event)) *Service {
	return &Service{sessions: map[string]*session{}, publish: publish, now: time.Now}
}

// State reports a session's queue; an unknown session is an empty queue.
func (s *Service) State(id string) (State, error) {
	if !sessionRe.MatchString(id) {
		return State{}, ErrBadSession
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ses, ok := s.sessions[id]; ok {
		ses.touched = s.now()
		return ses.q.State(), nil
	}
	return NewQueue(nil).State(), nil
}

// Update applies change to a session's queue, creating it if need be, and
// returns the result. A change that alters the queue is published.
func (s *Service) Update(id string, change func(*Queue) error) (State, error) {
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
	before := ses.q.revision
	err := change(ses.q)
	st := ses.q.State()
	s.mu.Unlock()
	if err != nil {
		return st, err
	}
	if st.Revision != before && s.publish != nil {
		s.publish(Event{Session: id, Revision: st.Revision})
	}
	return st, nil
}

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
