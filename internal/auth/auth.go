// Package auth models identity for Reverb's household owner model. There is one
// local owner ("local") who holds every capability and owns the canonical
// library. Paired devices sync via Bearer tokens (see internal/sync/pairing.go)
// and P2P peer trust (see internal/p2p) but do not create separate user rows:
// the users table exists only as the FK target for attribution columns
// (download_jobs.initiated_by, synced_playlists.owner_user_id).
package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uhhhm/reverb/internal/store/db"
)

// OwnerID is the stable identity of the single local user.
const OwnerID = "local"

// CurrentUser is the authenticated identity attached to every local request.
// For browser requests via loopback, requireAuth always injects LocalUser();
// paired devices authenticate to /sync and P2P via Bearer tokens / peer IDs.
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

// LocalUser is the household owner every local request is attributed to. The
// owner holds every capability, so capability-gated routes are open to it.
// Paired-device sync requests are attributed via sync device IDs, not this.
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

// Querier is the persistence slice the owner Service needs.
// *db.Queries satisfies it.
type Querier interface {
	GetUserByID(ctx context.Context, id string) (db.User, error)
	CreateUser(ctx context.Context, arg db.CreateUserParams) error
}

// Service seeds and reports on the single household owner. It has no password
// login; multi-device access is via pairing codes and sync tokens (see
// internal/sync).
type Service struct {
	q   Querier
	now func() time.Time
}

// NewService constructs the owner Service.
func NewService(q Querier, now func() time.Time) *Service {
	return &Service{q: q, now: now}
}

// EnsureSeed ensures the household owner row ("local") exists. It is idempotent:
// each startup checks for the local row by ID and creates it only when missing,
// so a database migrated from the old multi-user schema converges to the same
// state as a fresh install even if the migration's own seed did not run.
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
