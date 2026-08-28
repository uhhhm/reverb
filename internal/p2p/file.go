package p2p

// FileManifest mirrors the file_manifest table for the file sync protocol.
// Phase 4 will implement want/have over /reverb/file/1.0.0.
type FileManifest struct {
	CanonicalID string
	ContentHash string
	Size        int64
	RelPath     string
	Mtime       int64
	DeviceID    string
}
