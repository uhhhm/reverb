// Package notinterested owns the owner's Not interested marks: tracks and
// artists that must never be recommended.
//
// A mark is a replicated fact. It travels as one LWW field ("mark") on entity
// "notInterested" under a key every device derives the same way, and an undo
// writes null rather than a tombstone so that a later re-mark can still win.
// Local commands write the table and emit; Apply, used by the sync projection,
// only writes, so a peer's mark never echoes back.
package notinterested

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	"github.com/uhhhm/reverb/internal/override"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// Kind is what a mark rejects.
type Kind string

const (
	KindTrack  Kind = "track"
	KindArtist Kind = "artist"
)

// ErrInvalid is returned for a mark with no key or an unknown kind.
var ErrInvalid = errors.New("notinterested: invalid mark")

// Mark is one Not interested mark. Title, Artist, Source and ExternalID are
// what the Settings list shows; Key is the identity.
type Mark struct {
	Key        string `json:"key"`
	Kind       Kind   `json:"kind"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist"`
	Source     string `json:"source,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
	MarkedAt   int64  `json:"markedAt"`
}

// TrackMark marks a search-source track, keyed on (source, external id),
// which every device agrees on.
func TrackMark(source, externalID, title, artist string) Mark {
	return Mark{Key: trackKey(source + "\x00" + externalID), Kind: KindTrack, Title: title, Artist: artist, Source: source, ExternalID: externalID}
}

// LibraryTrackMark marks a library track. Its backend id belongs to one
// library backend and each device mints its own catalog id, so the key is the
// metadata fingerprint instead — the same rule playlist members follow. Pass
// the library's original names, not ones a rename has rewritten.
func LibraryTrackMark(title, artist, album string, durationMs int) Mark {
	return Mark{Key: trackKey("norm\x00" + matching.Fingerprint(title, artist, album, durationMs)), Kind: KindTrack, Title: title, Artist: artist, Source: "library"}
}

// ArtistMark marks an artist, keyed on the name-derived artist key that album
// and artist renames already replicate under.
func ArtistMark(name string) Mark {
	return Mark{Key: "artist:" + override.ArtistKey(name), Kind: KindArtist, Artist: name}
}

func trackKey(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return "track:" + hex.EncodeToString(sum[:])[:32]
}

// Store is the not_interested table. *db.Queries satisfies it.
type Store interface {
	UpsertNotInterested(ctx context.Context, arg db.UpsertNotInterestedParams) error
	DeleteNotInterested(ctx context.Context, key string) error
	ListNotInterested(ctx context.Context) ([]db.NotInterested, error)
}

// Emitter publishes a mark to paired devices. *syncemit.Service satisfies it.
type Emitter interface {
	EmitEntityField(ctx context.Context, entityType, key, field string, value any)
}

type Service struct {
	store Store
	emit  Emitter
	now   func() time.Time
}

// New builds a Service. emit may be nil on a device that replicates nothing.
func New(store Store, emit Emitter) *Service {
	return &Service{store: store, emit: emit, now: time.Now}
}

// Mark records a mark made on this device and publishes it.
func (s *Service) Mark(ctx context.Context, m Mark) (Mark, error) {
	if m.Key == "" || (m.Kind != KindTrack && m.Kind != KindArtist) {
		return Mark{}, ErrInvalid
	}
	m.MarkedAt = s.now().UnixMilli()
	if err := s.write(ctx, m); err != nil {
		return Mark{}, err
	}
	if s.emit != nil {
		s.emit.EmitEntityField(ctx, reverbsync.EntityNotInterested, m.Key, reverbsync.FieldMark, m)
	}
	return m, nil
}

// Undo removes a mark on this device and publishes the removal.
func (s *Service) Undo(ctx context.Context, key string) error {
	if key == "" {
		return ErrInvalid
	}
	if err := s.store.DeleteNotInterested(ctx, key); err != nil {
		return err
	}
	if s.emit != nil {
		s.emit.EmitEntityField(ctx, reverbsync.EntityNotInterested, key, reverbsync.FieldMark, nil)
	}
	return nil
}

// Apply records a mark a peer made, or removes it when m is nil. It never
// emits: the change is already in the log.
func (s *Service) Apply(ctx context.Context, key string, m *Mark) error {
	if m == nil {
		return s.store.DeleteNotInterested(ctx, key)
	}
	m.Key = key
	return s.write(ctx, *m)
}

func (s *Service) write(ctx context.Context, m Mark) error {
	return s.store.UpsertNotInterested(ctx, db.UpsertNotInterestedParams{
		Key: m.Key, Kind: string(m.Kind), Title: m.Title, Artist: m.Artist,
		Source: m.Source, ExternalID: m.ExternalID, MarkedAt: m.MarkedAt,
	})
}

// List returns every mark, most recent first.
func (s *Service) List(ctx context.Context) ([]Mark, error) {
	rows, err := s.store.ListNotInterested(ctx)
	if err != nil {
		return nil, err
	}
	marks := make([]Mark, 0, len(rows))
	for _, r := range rows {
		marks = append(marks, Mark{Key: r.Key, Kind: Kind(r.Kind), Title: r.Title, Artist: r.Artist, Source: r.Source, ExternalID: r.ExternalID, MarkedAt: r.MarkedAt})
	}
	return marks, nil
}

// Set is every mark, ready to filter recommendations with. The zero Set
// excludes nothing.
type Set struct {
	keys    map[string]bool
	names   map[string]bool
	artists map[string]bool
}

// Set loads the current marks.
func (s *Service) Set(ctx context.Context) (Set, error) {
	marks, err := s.List(ctx)
	if err != nil {
		return Set{}, err
	}
	set := Set{keys: map[string]bool{}, names: map[string]bool{}, artists: map[string]bool{}}
	for _, m := range marks {
		set.keys[m.Key] = true
		switch m.Kind {
		case KindTrack:
			set.names[nameKey(m.Title, m.Artist)] = true
		case KindArtist:
			set.artists[override.ArtistKey(m.Artist)] = true
		}
	}
	return set, nil
}

// Track reports whether a track must not be recommended: it was marked, it is
// the same recording as a marked track on another source or in the library,
// or its artist was marked.
func (s Set) Track(ext core.ExternalResult) bool {
	if s.Artist(ext.Artist) || s.names[nameKey(ext.Title, ext.Artist)] {
		return true
	}
	if ext.Source == "library" {
		return s.keys[LibraryTrackMark(ext.Title, ext.Artist, ext.Album, ext.DurationMs).Key]
	}
	return s.keys[TrackMark(ext.Source, ext.ExternalID, ext.Title, ext.Artist).Key]
}

// Artist reports whether an artist was marked.
func (s Set) Artist(name string) bool {
	return s.artists[override.ArtistKey(name)]
}

// nameKey names a recording across sources: its primary artist and title, with
// version qualifiers kept so a marked studio cut does not hide the live one.
func nameKey(title, artist string) string {
	return override.ArtistKey(artist) + "\x1f" + matching.Normalize(title)
}
