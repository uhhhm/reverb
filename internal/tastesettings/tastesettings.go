// Package tastesettings owns the household's recommendation settings:
// Adventurousness and the Online recommendations switch.
//
// Both belong to the taste profile, so they replicate. Each is one LWW field
// on entity "tasteSettings" under a single household key. Local updates write
// the settings table and emit; Apply, used by the sync projection, only
// writes, so a peer's change never echoes back.
package tastesettings

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// HouseholdKey is the entity id the settings replicate under: there is one
// set per household.
const HouseholdKey = "household"

const (
	keyAdventurousness = "taste:adventurousness"
	keyOnline          = "taste:online_recommendations"
)

// ErrInvalid is returned for an Adventurousness outside 0 to 100.
var ErrInvalid = errors.New("tastesettings: adventurousness must be between 0 and 100")

// Settings are the household's recommendation settings.
type Settings struct {
	// Adventurousness, 0 to 100, shifts every surface's balance of new versus
	// known music around its default at 50.
	Adventurousness int `json:"adventurousness"`
	// OnlineRecommendations allows similarity lookups against Last.fm,
	// ListenBrainz and Deezer. Off, nothing is looked up and the surfaces that
	// need online data come back unavailable.
	OnlineRecommendations bool `json:"onlineRecommendations"`
}

// Defaults are the settings until the owner changes them.
func Defaults() Settings {
	return Settings{Adventurousness: 50, OnlineRecommendations: true}
}

// Patch changes the settings that are set.
type Patch struct {
	Adventurousness       *int  `json:"adventurousness,omitempty"`
	OnlineRecommendations *bool `json:"onlineRecommendations,omitempty"`
}

// Store is the settings table. *db.Queries satisfies it.
type Store interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error
}

// Emitter publishes a change to paired devices. *syncemit.Service satisfies it.
type Emitter interface {
	EmitEntityField(ctx context.Context, entityType, key, field string, value any)
}

type Service struct {
	store Store
	emit  Emitter
}

// New builds a Service. emit may be nil on a device that replicates nothing.
func New(store Store, emit Emitter) *Service {
	return &Service{store: store, emit: emit}
}

// Get returns the current settings, with defaults for anything never set.
func (s *Service) Get(ctx context.Context) (Settings, error) {
	out := Defaults()
	if v, ok, err := s.read(ctx, keyAdventurousness); err != nil {
		return Settings{}, err
	} else if n, perr := strconv.Atoi(v); ok && perr == nil {
		out.Adventurousness = n
	}
	if v, ok, err := s.read(ctx, keyOnline); err != nil {
		return Settings{}, err
	} else if b, perr := strconv.ParseBool(v); ok && perr == nil {
		out.OnlineRecommendations = b
	}
	return out, nil
}

// Update applies a patch made on this device and publishes each changed
// setting.
func (s *Service) Update(ctx context.Context, p Patch) (Settings, error) {
	if p.Adventurousness != nil && (*p.Adventurousness < 0 || *p.Adventurousness > 100) {
		return Settings{}, ErrInvalid
	}
	if p.Adventurousness != nil {
		if err := s.write(ctx, keyAdventurousness, strconv.Itoa(*p.Adventurousness)); err != nil {
			return Settings{}, err
		}
		s.publish(ctx, reverbsync.FieldAdventurousness, *p.Adventurousness)
	}
	if p.OnlineRecommendations != nil {
		if err := s.write(ctx, keyOnline, strconv.FormatBool(*p.OnlineRecommendations)); err != nil {
			return Settings{}, err
		}
		s.publish(ctx, reverbsync.FieldOnlineRecommendations, *p.OnlineRecommendations)
	}
	return s.Get(ctx)
}

// Apply records a setting a peer changed. It never emits: the change is
// already in the log. decode reads the replicated value into its argument.
func (s *Service) Apply(ctx context.Context, field string, decode func(target any) error) error {
	switch field {
	case reverbsync.FieldAdventurousness:
		var n int
		if err := decode(&n); err != nil {
			return err
		}
		return s.write(ctx, keyAdventurousness, strconv.Itoa(min(max(n, 0), 100)))
	case reverbsync.FieldOnlineRecommendations:
		var b bool
		if err := decode(&b); err != nil {
			return err
		}
		return s.write(ctx, keyOnline, strconv.FormatBool(b))
	}
	return nil
}

func (s *Service) publish(ctx context.Context, field string, value any) {
	if s.emit != nil {
		s.emit.EmitEntityField(ctx, reverbsync.EntityTasteSettings, HouseholdKey, field, value)
	}
}

func (s *Service) read(ctx context.Context, key string) (string, bool, error) {
	v, err := s.store.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *Service) write(ctx context.Context, key, value string) error {
	return s.store.UpsertSetting(ctx, db.UpsertSettingParams{Key: key, Value: value})
}
