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
}
