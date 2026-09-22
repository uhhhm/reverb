package p2p

// FileManifest mirrors the file_manifest table for the file sync protocol.
// It is the wire type for /reverb/manifest/1.0.0, so the json tags are part of
// the protocol — do not rename them.
type FileManifest struct {
	CanonicalID string `json:"canonicalId"`
	ContentHash string `json:"contentHash"`
	Size        int64  `json:"size"`
	RelPath     string `json:"relPath"`
	Mtime       int64  `json:"mtime"`
	DeviceID    string `json:"deviceId"`
	// What an audio file's tags say it is, so a device that keeps only some of
	// a peer's files can tell which ones a playlist names. Empty for other
	// files and from a peer that predates them.
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	ISRC   string `json:"isrc,omitempty"`
}
