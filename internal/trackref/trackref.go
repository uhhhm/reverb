// Package trackref owns track identity: how an external track is encoded as a
// synthetic id, how to tell an external id from a library id, and how to
// compute a cross-source dedup key.
//
// The load-bearing invariant "real library ids never contain a colon" used to
// live as a comment in api/extstream.go: isExternalTrackID did
// strings.Contains(id, ":"). External search results were synthesised as
// source:externalId in Search.tsx, and external streaming was routed by the
// externalStream field in the frontend but by the colon heuristic in the
// backend. This package is the single owner of that decision so the heuristic
// cannot drift between tiers.
package trackref

import (
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
)

// sep is the unit-separator joining dedup fields. It cannot appear in
// normalized text, so "a"+"bc" never collides with "ab"+"c".
const sep = "␟"

// EncodeExternalID synthesizes the display id for a track that is not in the
// library. The SPA uses this as Track.id when externalStream is set; the
// backend uses it as the extstream cache key.
func EncodeExternalID(source, externalID string) string {
	source = strings.TrimSpace(source)
	externalID = strings.TrimSpace(externalID)
	if source == "" || externalID == "" {
		return ""
	}
	return source + ":" + externalID
}

// DecodeExternalID splits a synthetic id back into source and externalID.
// It returns ok == false when id is not a well-formed external id.
func DecodeExternalID(id string) (source, externalID string, ok bool) {
	id = strings.TrimSpace(id)
	i := strings.Index(id, ":")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	source = strings.TrimSpace(id[:i])
	externalID = strings.TrimSpace(id[i+1:])
	if source == "" || externalID == "" {
		return "", "", false
	}
	// Source itself must not contain whitespace or colon (it is a registry name
	// like "spotify" or "deezer"). ExternalID may contain colons (e.g. Spotify
	// ids never do, but a synthetic future source could), so only the first
	// colon is the separator — the remainder is the external id verbatim.
	if strings.Contains(source, ":") || strings.Contains(source, " ") {
		return "", "", false
	}
	return source, externalID, true
}

// IsExternalID reports whether id is a synthetic external-track id.
func IsExternalID(id string) bool {
	_, _, ok := DecodeExternalID(id)
	return ok
}

// DedupKey computes the cross-source dedup key for an external result.
//
// When an ISRC is present it is authoritative and lowercased: "isrc:<lower>".
// Otherwise the key is "nf:<norm(artist)>␟<norm(title)>" using
// matching.Normalize, which is the single shared normalization on the backend.
// The frontend's trackRef.dedupKey mirrors this exactly (diacritic fold,
// feat stripping, pt→part, paren-preserving) so search results deduped in
// the browser and on the server agree.
//
// This is the user-visible "same song" identity, not the download dedup hash
// (download.DedupKey hashes + folds in quality and section). For download
// identity, use download.DedupKey which now delegates to this for the base.
func DedupKey(r core.ExternalResult) string {
	if isrc := strings.TrimSpace(r.ISRC); isrc != "" {
		return "isrc:" + strings.ToLower(isrc)
	}
	return "nf:" + matching.Normalize(r.Artist) + sep + matching.Normalize(r.Title)
}

// DedupKeyForTrack computes the same key for a library track, so a library
// search hit and an external result can be compared. The library Track and the
// external result form of the same song must produce the same key.
func DedupKeyForTrack(t core.Track) string {
	if isrc := strings.TrimSpace(t.ISRC); isrc != "" {
		return "isrc:" + strings.ToLower(isrc)
	}
	return "nf:" + matching.Normalize(t.Artist) + sep + matching.Normalize(t.Title)
}

// ExternalCacheKey is the extstream cache key for a source+external id.
// It is EncodeExternalID with trimming, kept as a named helper so the
// extstream cache cannot drift from the id synthesis in the SPA.
func ExternalCacheKey(source, externalID string) string {
	return EncodeExternalID(source, externalID)
}
