package playlistsync

import "context"

// Emitter publishes a playlist's current state to the sync log so paired
// devices receive it.
//
// It is handed the id rather than the change, and re-derives the diff itself.
// That way every mutation on this Service publishes through one call and none
// of them has to describe what it did — a new mutator that forgets to say "I
// added a track" still replicates correctly.
type Emitter interface {
	Publish(ctx context.Context, playlistID string)
}

// WithEmitter attaches the sync emitter. Nil-safe: without one the Service
// still works, playlists just stay on this device.
func (s *Service) WithEmitter(e Emitter) *Service {
	s.emitter = e
	return s
}

// publish is best-effort by design: a device with no peers has nothing to
// publish to, and a replication failure must never fail the user's edit.
func (s *Service) publish(ctx context.Context, id string) {
	if s != nil && s.emitter != nil && id != "" {
		s.emitter.Publish(ctx, id)
	}
}
