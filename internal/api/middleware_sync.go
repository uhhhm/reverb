package api

import (
	"context"
	"database/sql"
	"net"
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
	// No Bearer token: fall back to the server device only for a request that
	// arrived over loopback. The browser UI is implicitly the household owner, so
	// requireAuth puts LocalUser in the context of every request it wraps -- that
	// alone proves nothing about who sent it. The listener defaults to loopback,
	// but the bind address is configurable, so without this transport check any
	// host that can reach a widened listener could author sync changes as the
	// server device with no pairing token at all. Loopback keeps
	// the built-in UI (desktop and locally-browsed server) working unpaired while
	// remote paired devices must present a Bearer sync token.
	if _, ok := currentUser(r); !ok {
		return "", sync.ErrInvalidToken
	}
	if !isLoopbackRequest(r) {
		return "", sync.ErrInvalidToken
	}
	id, err := s.syncServerDeviceID(r.Context())
	if err != nil {
		return "", err
	}
	return id, nil
}

// isLoopbackRequest reports whether the request came from the local machine.
// It reads the transport peer address only: X-Forwarded-For and friends are
// attacker-controlled, so trusting them here would reopen the bypass.
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "" {
		return false
	}
	// A unix-socket or in-process listener reports no usable peer address.
	if host == "@" || strings.HasPrefix(host, "/") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
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
