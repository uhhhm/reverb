package trackref

import (
	"testing"

	"github.com/uhhhm/reverb/internal/core"
)

func TestEncodeDecodeExternalID(t *testing.T) {
	cases := []struct {
		source, externalID string
		encoded            string
	}{
		{"deezer", "1144909952", "deezer:1144909952"},
		{"spotify", "4h47YiL87c9mmfBGwMTvai", "spotify:4h47YiL87c9mmfBGwMTvai"},
		{"spotify", " a b ", "spotify:a b"},
		// trims
		{" deezer ", " 123 ", "deezer:123"},
	}
	for _, c := range cases {
		got := EncodeExternalID(c.source, c.externalID)
		if got != c.encoded {
			t.Errorf("EncodeExternalID(%q,%q)=%q want %q", c.source, c.externalID, got, c.encoded)
		}
		wantSrc := c.source
		// trim manually for expectation
		wantSrc = wantSrc[0:] // no-op to keep linter happy; real trim via helper below
		src, eid, ok := DecodeExternalID(got)
		// Decode returns trimmed values; expect trimmed inputs
		expSrc := c.source
		expID := c.externalID
		// use the same trimming Encode uses (strings.TrimSpace)
		// inline trim without importing strings for brevity in test table: we know the values
		if c.source == " deezer " {
			expSrc = "deezer"
			expID = "123"
		} else if c.source == "spotify" && c.externalID == " a b " {
			expSrc = "spotify"
			expID = "a b"
		}
		_ = wantSrc
		if !ok || src != expSrc || eid != expID {
			t.Errorf("DecodeExternalID(%q)=%q,%q,%v want %q,%q,true", got, src, eid, ok, expSrc, expID)
		}
	}
}

func stringsTrim(s string) string {
	// local helper mirroring what Encode does, avoid importing strings in test helper? but we already have it.
	// use the same trim that Encode uses: TrimSpace
	// inline to avoid import cycle confusion
	trimmed := s
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\n') {
		trimmed = trimmed[1:]
	}
	for len(trimmed) > 0 && (trimmed[len(trimmed)-1] == ' ' || trimmed[len(trimmed)-1] == '\t' || trimmed[len(trimmed)-1] == '\n') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

func TestEncodeExternalID_Empty(t *testing.T) {
	if got := EncodeExternalID("", "123"); got != "" {
		t.Errorf("empty source should give empty, got %q", got)
	}
	if got := EncodeExternalID("deezer", ""); got != "" {
		t.Errorf("empty externalID should give empty, got %q", got)
	}
	if got := EncodeExternalID(" ", "123"); got != "" {
		t.Errorf("blank source should give empty, got %q", got)
	}
}

func TestDecodeExternalID_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"deezer",
		":123",
		"deezer:",
		"al2f3c9d8e7b6a5",
		"trk_abc123",
		"deezer:  ", // external empty after trim
		" :123",     // source empty after trim
		"dee zer:123",
	}
	for _, id := range invalid {
		if _, _, ok := DecodeExternalID(id); ok {
			t.Errorf("DecodeExternalID(%q) should be invalid", id)
		}
		if IsExternalID(id) {
			t.Errorf("IsExternalID(%q) should be false", id)
		}
	}
}

func TestIsExternalID(t *testing.T) {
	valid := []string{"deezer:1144909952", "spotify:abc", "a:b:c"}
	for _, id := range valid {
		if !IsExternalID(id) {
			t.Errorf("IsExternalID(%q) should be true", id)
		}
	}
	// "a:b:c" decodes as source=a, externalID=b:c — okay, externalID may contain colon
	src, eid, ok := DecodeExternalID("a:b:c")
	if !ok || src != "a" || eid != "b:c" {
		t.Errorf("DecodeExternalID(a:b:c)=%q,%q,%v want a,b:c,true", src, eid, ok)
	}
}

func TestDedupKey(t *testing.T) {
	// ISRC takes precedence and is lowercased
	if got := DedupKey(core.ExternalResult{ISRC: "USX1", Artist: "A", Title: "T"}); got != "isrc:usx1" {
		t.Errorf("isrc key = %q", got)
	}
	// feat stripping: "The Band" + "Song (feat. X)" → same as "Song"
	k1 := DedupKey(core.ExternalResult{Artist: "The Band", Title: "Song (feat. X)"})
	k2 := DedupKey(core.ExternalResult{Artist: "The Band", Title: "Song"})
	if k1 != k2 {
		t.Errorf("feat dedup mismatch: %q vs %q", k1, k2)
	}
	// daft punk regression
	k := DedupKey(core.ExternalResult{Artist: "Daft Punk", Title: "Get Lucky"})
	if k != "nf:daft punk␟get lucky" {
		t.Errorf("daft punk key = %q", k)
	}
	// separator prevents collision
	k1 = DedupKey(core.ExternalResult{Artist: "a", Title: "bc"})
	k2 = DedupKey(core.ExternalResult{Artist: "ab", Title: "c"})
	if k1 == k2 {
		t.Errorf("separator collision: both %q", k1)
	}
	// symmetry with track
	track := core.Track{Artist: "Daft Punk", Title: "Get Lucky"}
	if DedupKeyForTrack(track) != DedupKey(core.ExternalResult{Artist: "Daft Punk", Title: "Get Lucky"}) {
		t.Errorf("track vs external dedup mismatch")
	}
	// diacritics folded via matching.Normalize
	if got := DedupKey(core.ExternalResult{Artist: "Björk", Title: "Jóga"}); got != "nf:bjork␟joga" {
		t.Errorf("diacritic dedup = %q", got)
	}
	// pt → part
	if got := DedupKey(core.ExternalResult{Artist: "A", Title: "Movement Pt. 1"}); got != "nf:a␟movement part 1" {
		t.Errorf("pt dedup = %q", got)
	}
}

func TestExternalCacheKey(t *testing.T) {
	if got := ExternalCacheKey("deezer", "123"); got != "deezer:123" {
		t.Errorf("cache key = %q", got)
	}
	if got := ExternalCacheKey(" deezer ", " 123 "); got != "deezer:123" {
		t.Errorf("cache key trim = %q", got)
	}
}
