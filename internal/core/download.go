package core

// DownloadGranularity describes what unit a downloader operates on.
// GranularityTrack downloaders (e.g. spotDL) fetch individual songs.
// GranularityAlbum downloaders (e.g. Lidarr) fetch whole albums and are
// excluded from the per-track fallback chain.
type DownloadGranularity string

const (
	GranularityTrack DownloadGranularity = "track"
	GranularityAlbum DownloadGranularity = "album"
)

// DownloadStatus is the lifecycle state of a DownloadJob.
type DownloadStatus string

const (
	DownloadQueued    DownloadStatus = "queued"
	DownloadRunning   DownloadStatus = "running"
	DownloadCompleted DownloadStatus = "completed"
	DownloadFailed    DownloadStatus = "failed"
	DownloadCanceled  DownloadStatus = "canceled"
)

// DownloadRequest is built from an ExternalResult when the user clicks download.
type DownloadRequest struct {
	Source     string `json:"source"`
	ExternalID string `json:"externalId"`
	Artist     string `json:"artist"`
	Title      string `json:"title"`
	Album      string `json:"album"`
	ISRC       string `json:"isrc,omitempty"`
	// DurationMs from the originating search result; forwarded into the
	// post-download re-match so the fuzzy rung can disambiguate by length.
	DurationMs    int  `json:"durationMs,omitempty"`
	PlayWhenReady bool `json:"playWhenReady"`
	// ManualURL is an optional user-supplied source URL (e.g. a YouTube link) that
	// overrides the default query construction. When set alongside a Spotify source +
	// ExternalID, the spotDL adapter uses the pipe syntax
	// "https://open.spotify.com/track/<id>|<manualURL>" so Spotify metadata is
	// preserved while the audio is fetched from the manual URL.
	ManualURL string `json:"manualUrl,omitempty"`
	// AddToPlaylistID, when non-empty, causes the download manager to add the
	// resulting library track to this playlist ID once the download completes and
	// the track is matched in the library. Used by the one-time import path so
	// missing tracks are appended to the target playlist as each finishes.
	AddToPlaylistID string `json:"addToPlaylistId,omitempty"`
	// Granularity hints whether this is a track-level or album-level download
	// request. Empty defaults to GranularityTrack. Set by callers that want an
	// album-granularity downloader (e.g. Lidarr) for a full-album import.
	Granularity DownloadGranularity `json:"granularity,omitempty"`
	// Quality is the requested audio quality tier (a ceiling — see AudioQuality).
	// Empty means "use the download_quality setting".
	Quality AudioQuality `json:"quality,omitempty"`
	// SectionStart and SectionEnd trim the source to a time range, as accepted by
	// yt-dlp's --download-sections (e.g. "1:30", "00:01:30", "90"). Either may be
	// empty: an empty start means "from the beginning", an empty end "to the end".
	// Ignored by downloaders that cannot trim.
	SectionStart string `json:"sectionStart,omitempty"`
	SectionEnd   string `json:"sectionEnd,omitempty"`
	// ForceOverwrite tells the downloader to replace an existing file rather than
	// skip it. Set by the quality-upgrade path, where an identical filename
	// already sits in the output dir and skipping is exactly the wrong behaviour.
	ForceOverwrite bool `json:"forceOverwrite,omitempty"`
	// PreferDownloader names a downloader that should be tried first, ahead of the
	// configured order, when it is present and its CanDownload accepts the request.
	// Set server-side (hence json:"-") — e.g. a pasted YouTube link prefers "ytdlp"
	// over spotDL's Spotify-metadata-first flow. Falls back to the normal chain.
	PreferDownloader string `json:"-"`
	// InitiatedBy is the id of the user who initiated this download. It is set
	// server-side from the request context (never from the client body, hence
	// json:"-") and persisted on the job as download_jobs.initiated_by.
	InitiatedBy string `json:"-"`
}

// Chapter is one internal chapter of a source video, as reported by the
// downloader. Used to offer per-chapter splitting: each chapter becomes its own
// download request trimmed to StartSec..EndSec.
type Chapter struct {
	Title    string  `json:"title"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
}
