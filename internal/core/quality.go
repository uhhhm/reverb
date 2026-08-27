package core

import "strings"

// AudioQuality is the user-facing download quality tier.
//
// A tier is a CEILING, never a target: YouTube (the audio source behind both
// spotDL and yt-dlp) tops out around 130-160 kbps Opus, so encoding that to
// 320 kbps mp3 would inflate the file without adding any information. Adapters
// therefore transcode DOWN to the tier when the source exceeds it and otherwise
// keep whatever the source served.
type AudioQuality string

const (
	QualityLow    AudioQuality = "low"    // 128 kbps ceiling
	QualityMedium AudioQuality = "medium" // 192 kbps ceiling
	QualityHigh   AudioQuality = "high"   // 320 kbps ceiling
	// QualityBest keeps the source codec untouched — no re-encode at all.
	QualityBest AudioQuality = "best"
)

// DefaultAudioQuality is what a download uses when neither the request nor the
// download_quality setting specifies one.
const DefaultAudioQuality = QualityHigh

// KbpsCeiling is the tier's bitrate ceiling in kbps; 0 means "no re-encode".
func (q AudioQuality) KbpsCeiling() int {
	switch q {
	case QualityLow:
		return 128
	case QualityMedium:
		return 192
	case QualityHigh:
		return 320
	default:
		return 0
	}
}

func (q AudioQuality) Valid() bool {
	switch q {
	case QualityLow, QualityMedium, QualityHigh, QualityBest:
		return true
	}
	return false
}

// Exceeds reports whether other is a strictly higher tier than q. Used to decide
// whether an already-downloaded track is worth re-fetching.
func (q AudioQuality) Exceeds(other AudioQuality) bool {
	return q.rank() > other.rank()
}

func (q AudioQuality) rank() int {
	switch q {
	case QualityLow:
		return 1
	case QualityMedium:
		return 2
	case QualityHigh:
		return 3
	case QualityBest:
		return 4
	}
	return 0
}

// ParseAudioQuality normalises user input, falling back to def for anything
// unrecognised (including empty).
func ParseAudioQuality(s string, def AudioQuality) AudioQuality {
	q := AudioQuality(strings.ToLower(strings.TrimSpace(s)))
	if q.Valid() {
		return q
	}
	return def
}

// QualityForBitrate classifies an existing file's bitrate into the lowest tier
// that could have produced it, so the UI can show what a library track is and
// spot the ones worth upgrading. A bitrate at or above the high ceiling is
// reported as QualityHigh — "best" is about skipping the re-encode, which a
// bitrate alone cannot tell us.
func QualityForBitrate(kbps int) AudioQuality {
	switch {
	case kbps <= 0:
		return ""
	case kbps <= QualityLow.KbpsCeiling():
		return QualityLow
	case kbps <= QualityMedium.KbpsCeiling():
		return QualityMedium
	default:
		return QualityHigh
	}
}
