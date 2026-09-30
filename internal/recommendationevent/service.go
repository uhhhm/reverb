// Package recommendationevent records when a recommendation causes a durable
// addition to the canonical library or a managed playlist.
package recommendationevent

import (
	"context"
	"errors"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/store/db"
	"github.com/uhhhm/reverb/internal/syncemit"
)

const (
	ActionLibrary  = "library"
	ActionPlaylist = "playlist"
)

var ErrInvalid = errors.New("invalid recommendation attribution")

type Store interface {
	InsertRecommendationAddIfAbsent(context.Context, db.InsertRecommendationAddIfAbsentParams) error
	GetRecommendationAdd(context.Context, string) (db.RecommendationAdd, error)
}

type Emitter interface {
	EmitRecommendationAdd(context.Context, string, syncemit.RecommendationAdd)
}

type Service struct {
	q       Store
	emitter Emitter
	now     func() time.Time
	idgen   func() string
}

func New(q Store, emitter Emitter, now func() time.Time, idgen func() string) *Service {
	return &Service{q: q, emitter: emitter, now: now, idgen: idgen}
}

func ValidOrigin(origin string) bool {
	return core.RecommendationOrigin(origin).Valid()
}

func (s *Service) Record(ctx context.Context, userID, origin, action string) error {
	return s.RecordWithID(ctx, s.idgen(), userID, origin, action)
}

// RecordWithID retries one durable addition under the caller's stable identity.
// Read the stored record after insert so repeated emissions retain its original
// timestamp and attribution even when a completion's final job write failed.
func (s *Service) RecordWithID(ctx context.Context, id, userID, origin, action string) error {
	if id == "" || !ValidOrigin(origin) || action != ActionLibrary && action != ActionPlaylist {
		return ErrInvalid
	}
	if err := s.q.InsertRecommendationAddIfAbsent(ctx, db.InsertRecommendationAddIfAbsentParams{
		ID: id, UserID: userID, Origin: origin, Action: action, CreatedAt: s.now().Unix(),
	}); err != nil {
		return err
	}
	stored, err := s.q.GetRecommendationAdd(ctx, id)
	if err != nil {
		return err
	}
	if s.emitter != nil {
		s.emitter.EmitRecommendationAdd(ctx, id, syncemit.RecommendationAdd{UserID: stored.UserID, Origin: stored.Origin, Action: stored.Action, CreatedAt: stored.CreatedAt})
	}
	return nil
}
