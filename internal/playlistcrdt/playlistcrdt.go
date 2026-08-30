// Package playlistcrdt replicates managed playlists between paired devices.
//
// A playlist is not one value. Replicating it as one would mean that adding a
// track on a phone and adding a different track on a laptop, before the two
// meet, ends with one of the additions silently gone. So each membership is its
// own field in the change log — concurrent additions of different tracks both
// survive — and position is carried as a fractional order key, so moving one
// track rewrites one key instead of renumbering the list and clobbering an
// unrelated move.
//
// Only mode="once" playlists replicate their tracklist. A mode="synced"
// playlist is a mirror of a Spotify/Deezer playlist that every device rebuilds
// for itself from the same upstream, so replicating its contents would be churn
// with no effect; its identity and settings still travel, so importing it on
// one device makes it appear on the others.
package playlistcrdt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// Playlist-level fields. Each is independently last-writer-wins, so renaming a
// playlist on one device and re-covering it on another keeps both edits.
const (
	FieldName            = "name"
	FieldCoverURL        = "coverUrl"
	FieldMode            = "mode"
	FieldSource          = "source"
	FieldExternalID      = "externalId"
	FieldSyncEnabled     = "syncEnabled"
	FieldSyncIntervalSec = "syncIntervalSec"
	FieldAutoDownload    = "autoDownload"
	FieldCreatedAt       = "createdAt"
)

// memberPrefix marks the fields that carry one track's membership. The rest of
// the field name is a digest of the track's identity, so the same track always
// lands on the same field and two devices adding it independently converge
// instead of duplicating it.
const memberPrefix = "track:"

// member is the value of one membership field. A removal keeps the field and
// clears Present rather than dropping it: the log has no way to express "this
// field is gone", and a removal has to be able to beat an older addition.
type member struct {
	Present bool                 `json:"present"`
	Order   string               `json:"order,omitempty"`
	Entry   *core.ExternalResult `json:"entry,omitempty"`
}

// MemberKey is the field suffix identifying one track within a playlist.
//
// For a track from a search source, the (source, externalID) pair is the
// identity and every device agrees on it. A library track's id is not: it
// belongs to one library backend, and two devices index the same file under
// different ids — hashing it would make one track look like two after a merge.
// So a library track is keyed on the metadata fingerprint instead, the same one
// the catalog uses to decide that two library entries are the same recording.
func MemberKey(e core.ExternalResult) string {
	identity := e.Source + "\x00" + e.ExternalID
	if e.Source == "library" {
		identity = "norm\x00" + matching.Fingerprint(e.Title, e.Artist, e.Album, e.DurationMs)
	}
	sum := sha256.Sum256([]byte(identity))
	return memberPrefix + hex.EncodeToString(sum[:])[:16]
}

// published is the form of an entry that travels.
//
// The catalog id is stripped: it is this device's own addressing for the track,
// minted from a random token, so it means nothing to a peer. Worse, carrying it
// would make the entry differ between the two devices forever, and every local
// edit would rewrite every member of the playlist to swap one id for the other.
func published(e core.ExternalResult) core.ExternalResult {
	e.CanonicalID = ""
	return e
}

// state is a playlist as the change log currently has it.
type state struct {
	deleted bool
	fields  map[string]any
	members map[string]member
}

func readState(changes []reverbsync.SyncChange) state {
	st := state{fields: map[string]any{}, members: map[string]member{}}
	for _, ch := range changes {
		if ch.Field == reverbsync.FieldDeleted {
			st.deleted = true
			continue
		}
		if strings.HasPrefix(ch.Field, memberPrefix) {
			// The persisted bytes are preferred; a change appended in-process
			// carries only the Go value.
			raw := []byte(ch.ValueJSON)
			if len(raw) == 0 {
				raw, _ = json.Marshal(ch.Value)
			}
			var m member
			if json.Unmarshal(raw, &m) == nil {
				st.members[ch.Field] = m
			}
			continue
		}
		st.fields[ch.Field] = ch.Value
	}
	return st
}

// orderedMembers returns the present members in playlist order. Ties on the
// order key are broken by field name so every device lands on the same list.
func (s state) orderedMembers() []string {
	keys := make([]string, 0, len(s.members))
	for k, m := range s.members {
		if m.Present {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := s.members[keys[i]].Order, s.members[keys[j]].Order
		if a != b {
			return a < b
		}
		return keys[i] < keys[j]
	})
	return keys
}

func (s state) orderKeys() map[string]string {
	out := make(map[string]string, len(s.members))
	for k, m := range s.members {
		if m.Present && m.Order != "" {
			out[k] = m.Order
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	}
	return 0
}

func boolean(v any) bool {
	b, _ := v.(bool)
	return b
}

func decodeTracks(tracksJSON string) []core.ExternalResult {
	var out []core.ExternalResult
	if tracksJSON != "" {
		_ = json.Unmarshal([]byte(tracksJSON), &out)
	}
	return out
}

// tracksReplicate reports whether a playlist's membership travels. See the
// package comment: a mirrored playlist rebuilds itself from upstream.
func tracksReplicate(mode string) bool { return mode != "synced" }
