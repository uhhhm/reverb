package playlistsync

import (
	"context"
	"sync"
)

// EditLocks serializes read-modify-write edits of one playlist. A playlist row
// holds its whole tracklist, so an edit reads the row, changes it and writes it
// back; two edits running at once would each write back only their own change.
//
// Locks are per playlist id, so edits of different playlists run in parallel.
// Adapter reload builds a new Service while edits on the old one may still be
// running, so the composition root owns one EditLocks and hands it to every
// Service it builds (see WithEditLocks).
type EditLocks struct {
	mu    sync.Mutex
	locks map[string]*editLock
}

type editLock struct {
	mu   sync.Mutex
	refs int // callers holding or waiting for mu; the entry is dropped at zero
}

func NewEditLocks() *EditLocks {
	return &EditLocks{locks: make(map[string]*editLock)}
}

// lock blocks until the caller holds the lock for id and returns its release.
func (l *EditLocks) lock(id string) (unlock func()) {
	l.mu.Lock()
	el := l.locks[id]
	if el == nil {
		el = &editLock{}
		l.locks[id] = el
	}
	el.refs++
	l.mu.Unlock()

	el.mu.Lock()
	return func() {
		el.mu.Unlock()
		l.mu.Lock()
		el.refs--
		if el.refs == 0 {
			delete(l.locks, id)
		}
		l.mu.Unlock()
	}
}

// WithEditLocks replaces the Service's own locks with shared ones, so a
// Service rebuilt by adapter reload and its predecessor do not edit the same
// playlist at once. Returns the receiver for chaining.
func (s *Service) WithEditLocks(l *EditLocks) *Service {
	if l != nil {
		s.locks = l
	}
	return s
}

// edit runs one read-modify-write of playlist id under its edit lock. apply
// changes row in place and reports whether to write it; when it does, the new
// name, cover and tracklist are stored and published before the lock is
// released, so the sync log sees edits in the order they were stored.
func (s *Service) edit(ctx context.Context, id string, apply func(row *SyncedRow) (bool, error)) error {
	unlock := s.locks.lock(id)
	defer unlock()
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	write, err := apply(&row)
	if err != nil || !write {
		return err
	}
	if err := s.store.UpdateTracks(ctx, id, row.Name, row.CoverURL, row.TracksJSON, s.now()); err != nil {
		return err
	}
	s.publish(ctx, id)
	return nil
}
