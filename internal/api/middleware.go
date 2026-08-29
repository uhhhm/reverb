package api

import (
	"context"
	"net/http"

	"github.com/uhhhm/reverb/internal/auth"
)

type ctxKey int

const userCtxKey ctxKey = iota

// currentUser returns the authenticated user injected by requireAuth.
func currentUser(r *http.Request) (auth.CurrentUser, bool) {
	cu, ok := r.Context().Value(userCtxKey).(auth.CurrentUser)
	return cu, ok
}

// requireAuth authenticates every local request as the household owner.
// Browser UI requests arriving over loopback are implicitly the owner; paired
// devices use Bearer tokens on /sync and peer IDs on P2P, not this middleware.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cu := auth.LocalUser()
		ctx := context.WithValue(r.Context(), userCtxKey, cu)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireCapability gates a handler on the current user holding a capability.
// The household owner holds every capability, so local UI gates always pass;
// paired-device requests are scoped to capabilities via the same identity.
func (s *Server) requireCapability(cap string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cu, ok := currentUser(r)
			if !ok || !cu.Has(cap) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireCapability(auth.CapAdmin)(next)
}
