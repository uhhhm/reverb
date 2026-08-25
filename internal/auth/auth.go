// Package auth models identity for Reverb's single-user, no-login model. There
// is exactly one local user ("local"), who owns every resource and holds every
// capability. The users table exists only as the FK target for attribution
// columns (download_jobs.initiated_by, synced_playlists.owner_user_id).
package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/maxjb-xyz/reverb/internal/store/db"
)

// OwnerID is the stable identity of the single local user.
const OwnerID = "local"

// CurrentUser is the authenticated identity attached to every request. With a
// single user, requireAuth always injects LocalUser().
type CurrentUser struct {
	ID        string
	Username  string
	RoleID    string
	RoleName  string
	IsOwner   bool
	CreatedAt int64
	Caps      map[string]bool
}

// Has reports whether the user holds the given capability.
func (u CurrentUser) Has(cap string) bool { return u.Caps[cap] }

// LocalUser is the single user every request is attributed to. The owner holds
// every capability, so capability-gated routes are open to it.
func LocalUser() CurrentUser {
	caps := make(map[string]bool)
	for _, c := range AllCapabilities() {
		caps[c.Key] = true
	}
	return CurrentUser{
		ID:       OwnerID,
		Username: "local",
		RoleID:   "role-admin",
		RoleName: "Admin",
		IsOwner:  true,
		Caps:     caps,
	}
}

// Querier is the persistence slice the single-user Service needs.
// *db.Queries satisfies it.
type Querier interface {
	GetUserByID(ctx context.Context, id string) (db.User, error)
	CreateUser(ctx context.Context, arg db.CreateUserParams) error
}

// Service seeds and reports on the single local user. It has no login, session,
// role, or user-management surface.
type Service struct {
	q   Querier
	now func() time.Time
}

// NewService constructs the single-user Service.
func NewService(q Querier, now func() time.Time) *Service {
	return &Service{q: q, now: now}
}

// EnsureSeed ensures the single local owner row exists. It is idempotent: each
// startup checks for the local row by ID and creates it only when missing, so a
// database migrated from the old multi-user schema converges to the same state
// as a fresh install even if the migration's own seed did not run.
func (s *Service) EnsureSeed(ctx context.Context) error {
	if _, err := s.q.GetUserByID(ctx, OwnerID); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.q.CreateUser(ctx, db.CreateUserParams{ID: OwnerID, Username: "local"})
}

// IsSetupRequired is always false: there is no setup step.
func (s *Service) IsSetupRequired(context.Context) (bool, error) { return false, nil }
