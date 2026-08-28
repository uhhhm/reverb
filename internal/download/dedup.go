package download

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
)

// dedupSep is the unit-separator rune (␟) joining the normalized fields, matching
// the spec's dedup_key definition. It cannot appear in normalized text.
const dedupSep = "␟"

// DedupKey computes the deduplication key for a download request. An external
// catalog identity is authoritative whenever it exists: title/artist metadata
// often differs between a search result, coverage result, and retry. Requests
// without an external id retain the normalized metadata fallback.
func DedupKey(req core.DownloadRequest) string {
	var raw string
	if source, externalID := strings.ToLower(strings.TrimSpace(req.Source)), strings.TrimSpace(req.ExternalID); source != "" && externalID != "" {
		raw = "external" + dedupSep + source + dedupSep + externalID
	} else {
		raw = matching.Normalize(req.Artist) + dedupSep +
			matching.Normalize(req.Title) + dedupSep +
			matching.Normalize(req.Album)
	}
	// A quality upgrade deliberately targets a track that already has a terminal
	// job. Folding the tier in gives each upgrade its own key, so it neither joins
	// the original download nor blocks a later upgrade to a higher tier, while
	// ordinary requests keep the historical key shape.
	if req.ForceOverwrite {
		raw += dedupSep + "upgrade" + dedupSep + string(req.Quality)
	}
	// A trimmed request produces a different audio file from the same source, so
	// it has its own identity: a chapter split enqueues one request per chapter,
	// all sharing a source/external id, and they must not collapse into one job.
	if start, end := strings.TrimSpace(req.SectionStart), strings.TrimSpace(req.SectionEnd); start != "" || end != "" {
		raw += dedupSep + "section" + dedupSep + start + dedupSep + end
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
