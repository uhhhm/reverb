package recommend

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/matching"
)

// recentPlayWindow is how far back a play keeps a track off discovery surfaces.
const recentPlayWindow = 30 * 24 * time.Hour

// surfacePolicy is what a recommendation surface declares about itself. A
// discovery surface exists to find something new, so it leaves out tracks the
// owner already has or has just heard; the rest (Radio, similar tracks) keep
// them. Every surface drops other versions and duplicate recordings.
type surfacePolicy struct {
	discovery bool
}

var (
	similarTracksSurface = surfacePolicy{}
	radioSurface         = surfacePolicy{}
)

// WithRecentPlays lists what was played since a time, for discovery surfaces.
func WithRecentPlays(load func(ctx context.Context, since time.Time) ([]TrackCandidate, error)) Option {
	return func(s *Service) { s.recentPlays = load }
}

// recentlyPlayed returns the recording keys played within recentPlayWindow.
func (s *Service) recentlyPlayed(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if s.recentPlays == nil {
		return out
	}
	plays, err := s.recentPlays(ctx, s.now().Add(-recentPlayWindow))
	if err != nil {
		log.Printf("recommend: reading recent plays: %v", err)
		return out
	}
	for _, p := range plays {
		out[recordingKey(p.Title, p.Artist)] = true
	}
	return out
}

// filterTracks applies a surface's rules to tracks in order. seeds are what
// the recommendations were asked for: they are never recommended back, and a
// version type (live, remix, ...) is only allowed when a seed is one too.
// Duplicate recordings collapse into the first slot, keeping a library copy
// over a streamed one. recent holds recording keys for discovery surfaces.
func filterTracks(tracks []core.ExternalResult, seeds []Seed, p surfacePolicy, recent map[string]bool) []core.ExternalResult {
	var allowed versionSet
	seedKeys := map[string]bool{}
	for _, sd := range seeds {
		if sd.Title == "" {
			continue
		}
		allowed |= versionKinds(sd.Title)
		seedKeys[recordingKey(sd.Title, sd.Artist)] = true
	}
	out := make([]core.ExternalResult, 0, len(tracks))
	slot := map[string]int{}
	for _, t := range tracks {
		if versionKinds(t.Title)&^allowed != 0 {
			continue
		}
		key := recordingKey(t.Title, t.Artist)
		if seedKeys[key] {
			continue
		}
		if p.discovery && (t.Source == "library" || recent[key]) {
			continue
		}
		if i, ok := slot[key]; ok {
			if t.Source == "library" && out[i].Source != "library" {
				out[i] = t
			}
			continue
		}
		slot[key] = len(out)
		out = append(out, t)
	}
	return out
}

// recordingKey identifies a recording across albums and reissues: the
// matcher's fingerprint over the primary artist and the title, with reissue
// qualifiers ("2011 Remaster", "Deluxe Edition") removed and album and length
// left out. Version qualifiers stay, so a live cut keeps its own key.
func recordingKey(title, artist string) string {
	return matching.Fingerprint(stripReissue(title), artist, "", 0)
}

// reissueWords are the only words a qualifier may hold for it to name a
// reissue of the same recording rather than a different version.
var reissueWords = map[string]bool{
	"remaster": true, "remastered": true, "deluxe": true, "edition": true,
	"version": true, "bonus": true, "track": true, "expanded": true,
	"anniversary": true, "digital": true, "digitally": true, "the": true,
	"special": true, "super": true, "collector's": true, "collectors": true,
}

var yearOrOrdinal = regexp.MustCompile(`^(\d{2,4}|\d+(st|nd|rd|th))$`)

func stripReissue(title string) string {
	base, quals := splitQualifiers(title)
	kept := []string{base}
	for _, q := range quals {
		if !isReissue(q) {
			kept = append(kept, "("+q+")")
		}
	}
	return strings.Join(kept, " ")
}

func isReissue(q string) bool {
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return false
	}
	named := false
	for _, w := range words {
		switch {
		case reissueWords[w]:
			named = named || w != "the" && w != "version" && w != "track"
		case yearOrOrdinal.MatchString(w):
		default:
			return false
		}
	}
	return named
}

// splitQualifiers separates a title into the song's name and its qualifiers:
// bracketed parts and anything after a spaced dash. "Song - Live at X (2011
// Remaster)" yields "Song" and ["Live at X", "2011 Remaster"].
func splitQualifiers(title string) (string, []string) {
	var quals []string
	var base strings.Builder
	depth := 0
	var cur strings.Builder
	for _, r := range title {
		switch r {
		case '(', '[', '{':
			if depth == 0 {
				cur.Reset()
			} else {
				cur.WriteRune(r)
			}
			depth++
		case ')', ']', '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 {
				if q := strings.TrimSpace(cur.String()); q != "" {
					quals = append(quals, q)
				}
			} else {
				cur.WriteRune(r)
			}
		default:
			if depth > 0 {
				cur.WriteRune(r)
			} else {
				base.WriteRune(r)
			}
		}
	}
	name := base.String()
	for _, sep := range []string{" - ", " – ", " — "} {
		if i := strings.Index(name, sep); i >= 0 {
			for _, q := range strings.Split(name[i+len(sep):], sep) {
				if q = strings.TrimSpace(q); q != "" {
					quals = append(quals, q)
				}
			}
			name = name[:i]
			break
		}
	}
	return strings.TrimSpace(name), quals
}

type versionKind uint8

const (
	versionLive versionKind = 1 << iota
	versionCover
	versionRemix
	versionKaraoke
	versionInstrumental
)

var versionOrder = []versionKind{versionLive, versionCover, versionRemix, versionKaraoke, versionInstrumental}

func (k versionKind) String() string {
	switch k {
	case versionLive:
		return "live"
	case versionCover:
		return "cover"
	case versionRemix:
		return "remix"
	case versionKaraoke:
		return "karaoke"
	case versionInstrumental:
		return "instrumental"
	}
	return "unknown"
}

// versionSet is a set of versionKinds.
type versionSet uint8

func (s versionSet) has(k versionKind) bool { return s&versionSet(k) != 0 }

func (s *versionSet) add(k versionKind) { *s |= versionSet(k) }

func (s versionSet) list() []versionKind {
	var out []versionKind
	for _, k := range versionOrder {
		if s.has(k) {
			out = append(out, k)
		}
	}
	return out
}

// mixNotRemix are "... Mix" qualifiers that name the original recording.
var mixNotRemix = map[string]bool{"original": true, "radio": true, "album": true, "single": true, "main": true, "clean": true, "explicit": true}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}']+`)

// versionKinds detects the version types a title's qualifiers name. Only the
// qualifiers are read, so "Live Forever" is a song, not a live version.
func versionKinds(title string) versionSet {
	_, quals := splitQualifiers(title)
	var set versionSet
	for _, q := range quals {
		lower := strings.ToLower(q)
		words := wordRe.FindAllString(lower, -1)
		for i, w := range words {
			switch w {
			case "live":
				set.add(versionLive)
			case "cover", "covers":
				set.add(versionCover)
			case "remix", "remixed", "rmx", "rework", "bootleg":
				set.add(versionRemix)
			case "mix":
				if i == 0 || !mixNotRemix[words[i-1]] {
					set.add(versionRemix)
				}
			case "karaoke":
				set.add(versionKaraoke)
			case "instrumental":
				set.add(versionInstrumental)
			}
		}
		if strings.Contains(lower, "in the style of") || strings.Contains(lower, "backing track") {
			set.add(versionKaraoke)
		}
		if strings.Contains(lower, "originally performed") {
			set.add(versionCover)
			set.add(versionKaraoke)
		}
	}
	return set
}
