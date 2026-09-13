// Package recommendationevent records when a recommendation causes a durable
// addition to the canonical library or a managed playlist.
package recommendationevent

import (
	"context"
	"errors"
	"time"

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
	switch origin {
	case "radio", "mix", "shelf", "similarTracks", "similarArtists":
		return true
	default:
		return false
	}
}

func (s *Service) Record(ctx context.Context, userID, origin, action string) error {
	if !ValidOrigin(origin) || action != ActionLibrary && action != ActionPlaylist {
		return ErrInvalid
	}
	id, at := s.idgen(), s.now().Unix()
	if err := s.q.InsertRecommendationAddIfAbsent(ctx, db.InsertRecommendationAddIfAbsentParams{
		ID: id, UserID: userID, Origin: origin, Action: action, CreatedAt: at,
	}); err != nil {
		return err
	}
	if s.emitter != nil {
		s.emitter.EmitRecommendationAdd(ctx, id, syncemit.RecommendationAdd{UserID: userID, Origin: origin, Action: action, CreatedAt: at})
	}
	return nil
}
