package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/uhhhm/reverb/internal/sync"
)

type syncDeviceKey int

const syncDeviceIDKey syncDeviceKey = iota

func (s *Server) authenticateSync(r *http.Request) (string, error) {
	hdr := r.Header.Get("Authorization")
	if strings.HasPrefix(hdr, "Bearer ") {
		token := strings.TrimSpace(strings.TrimPrefix(hdr, "Bearer "))
		if token == "" {
			return "", sync.ErrInvalidToken
		}
		if s.deps.Pairing == nil {
			return "", sync.ErrInvalidToken
		}
		dev, err := s.deps.Pairing.AuthenticateByToken(r.Context(), token)
		if err != nil {
			return "", err
		}
		return dev.ID, nil
	}
	// No Bearer token: allow server-device fallback only for locally-
	// authenticated requests. Reverb is single-user with no login; every
	// request that passes requireAuth has LocalUser in context, while
	// anonymous requests outside that group have none. Checking
	// currentUser (not raw Cookie existence) fixes fresh installs that
	// never set reverb_session and prevents bypass via Cookie: reverb_session=anything.
	if _, ok := currentUser(r); !ok {
		return "", sync.ErrInvalidToken
	}
	id, err := s.syncServerDeviceID(r.Context())
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Server) syncServerDeviceID(ctx context.Context) (string, error) {
	if s.deps.PairingStore != nil {
		if id, err := sync.ServerDeviceID(ctx, s.deps.PairingStore); err == nil {
			return id, nil
		}
	}
	if s.deps.OfflineSet != nil {
		if id, err := sync.ServerDeviceID(ctx, s.deps.OfflineSet); err == nil {
			return id, nil
		}
	}
	return "", sql.ErrNoRows
}

func syncDeviceIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(syncDeviceIDKey).(string)
	return v, ok
}

func withSyncDeviceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, syncDeviceIDKey, id)
}
