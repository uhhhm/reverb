package api

import (
	"encoding/json"
	"net/http"

	"github.com/uhhhm/reverb/internal/auth"
	"github.com/uhhhm/reverb/internal/registry"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONPending wraps a payload with the restart-to-apply flag so the client can
// surface the "Restart to apply" banner immediately after a mutation.
func writeJSONPending(w http.ResponseWriter, status int, v any, pending bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Data           any  `json:"data"`
		PendingRestart bool `json:"pendingRestart"`
	}{Data: v, PendingRestart: pending})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	cu, _ := currentUser(r)
	caps := make([]string, 0, len(cu.Caps))
	for _, c := range auth.AllCapabilities() {
		if cu.Caps[c.Key] {
			caps = append(caps, c.Key)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": cu.ID, "username": cu.Username, "roleId": cu.RoleID,
		"roleName": cu.RoleName, "isOwner": cu.IsOwner, "capabilities": caps,
		"createdAt": cu.CreatedAt,
	})
}

type adapterInfo struct {
	Type         string                `json:"type"`
	Name         string                `json:"name"`
	ConfigSchema registry.ConfigSchema `json:"configSchema"`
	Capabilities []string              `json:"capabilities"`
}

func (s *Server) handleAdaptersAvailable(w http.ResponseWriter, r *http.Request) {
	out := make([]adapterInfo, 0)
	for _, reg := range []*registry.Registry{s.deps.Lib, s.deps.Search, s.deps.Downloader} {
		if reg == nil {
			continue
		}
		for _, name := range reg.Names() {
			p, err := reg.Create(name)
			if err != nil {
				continue
			}
			out = append(out, adapterInfo{
				Type:         p.Type(),
				Name:         p.Name(),
				ConfigSchema: p.ConfigSchema(),
				Capabilities: registry.DescribeCapabilities(p),
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}
