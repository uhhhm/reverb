package api

import (
	"context"
	"net/http"

	"github.com/maxjb-xyz/reverb/internal/auth"
)

const sessionCookie = "reverb_session"

type ctxKey int

const userCtxKey ctxKey = iota

// currentUser returns the authenticated user injected by requireAuth.
func currentUser(r *http.Request) (auth.CurrentUser, bool) {
	cu, ok := r.Context().Value(userCtxKey).(auth.CurrentUser)
	return cu, ok
}

// requireAuth authenticates every request as the single local user. Reverb has
// no login, so there is nothing to verify: the one owner is always present.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cu := auth.LocalUser()
		ctx := context.WithValue(r.Context(), userCtxKey, cu)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireCapability gates a handler on the current user holding a capability.
// The single local user holds every capability, so these gates always pass.
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
