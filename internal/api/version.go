package api

import (
	"net/http"
	"sort"

	"github.com/uhhhm/reverb/internal/p2p"
	"github.com/uhhhm/reverb/internal/release"
)

// handleVersion reports the build version and the repository update checks
// poll (empty when updates are disabled), the protocol support window, each
// paired device's version and compatibility as last reached, and, on the
// phone, the newest release with an IPA. Empty Deps.Version (e.g. zero-value
// Deps in tests, or a build without -ldflags) reports "dev".
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	v := s.deps.Version
	if v == "" {
		v = "dev"
	}
	peers := []p2p.PeerVersion{}
	if h := s.p2pHost(); h != nil && s.deps.P2PGuard != nil {
		if guard := s.deps.P2PGuard(); guard != nil {
			trusted, err := guard.TrustedPeers(r.Context())
			if err == nil {
				for pid, id := range trusted {
					peers = append(peers, p2p.VersionOf(h.LibHost(), pid, id, v))
				}
			}
		}
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].PeerID < peers[j].PeerID })
	latest := release.Status{}
	if s.deps.PhoneRelease != nil {
		latest = s.deps.PhoneRelease.Status(r.Context())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": v, "updateRepo": s.deps.UpdateRepo, "supportWindow": p2p.SupportWindow, "peers": peers,
		"latestVersion": latest.LatestVersion, "sourceUrl": latest.SourceURL, "releaseUrl": latest.ReleaseURL,
	})
}
