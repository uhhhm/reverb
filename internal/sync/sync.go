package sync

// SyncRequest is the envelope a device sends when syncing.
// SinceRevision is legacy global revision cursor (still honored). Vector and
// SinceHLC are the P2P successors: Vector maps deviceID -> seq, SinceHLC is
// the max HLC peer has seen. New clients send Vector; old clients still send
// SinceRevision and receive revision-based outbound.
// DeviceID is the sender's local device ID (used for libp2p where there is no
// HTTP Bearer token; HTTP path ignores it and uses authenticateSync).
type SyncRequest struct {
	SinceRevision int64            `json:"sinceRevision"`
	SinceHLC      int64            `json:"sinceHlc,omitempty"`
	Vector        map[string]int64 `json:"vector,omitempty"`
	DeviceID      string           `json:"deviceId,omitempty"`
	Changes       []SyncChange     `json:"changes"`
	// Devices announces verification keys the peer may not have yet.
	Devices []DeviceAnnounce `json:"devices,omitempty"`
	// Error carries a refusal (unknown device, id mismatch, store failure).
	// Without it a failure reply decodes as a successful empty round and the
	// caller retries forever with no signal on either side.
	Error string `json:"error,omitempty"`
}

// SyncChange is a per-field mutation. Field=="__deleted" is the sentinel for deletion.
// HLC + Seq together form the P2P dot (deviceID + seq, ordered by HLC). HLC=0 means
// legacy row (pre-0029); UpdatedAt remains wall time for back-compat fallback.
type SyncChange struct {
	EntityType string `json:"entityType"`
	EntityID   string `json:"entityId"`
	Field      string `json:"field"`
	Value      any    `json:"value"`
	UpdatedAt  int64  `json:"updatedAt"`
	DeviceID   string `json:"deviceId,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
	HLC        int64  `json:"hlc,omitempty"`
	Seq        int64  `json:"seq,omitempty"`
	// ValueJSON is the exact persisted value_json. It travels on the wire so a
	// verifier reconstructs the signed bytes exactly, rather than re-marshaling
	// Value and risking a different encoding.
	ValueJSON string `json:"valueJson,omitempty"`
	// Sig is the author's base64 Ed25519 signature over the change.
	Sig string `json:"sig,omitempty"`
}

// DeviceAnnounce carries a device's verification key so peers can check
// changes authored by devices they never paired with directly.
type DeviceAnnounce struct {
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
	Name      string `json:"name,omitempty"`
}

// SyncResponse is returned by the server after merging inbound changes.
type SyncResponse struct {
	Changes     []SyncChange     `json:"changes"`
	NewRevision int64            `json:"newRevision"`
	NewHLC      int64            `json:"newHlc,omitempty"`
	Vector      map[string]int64 `json:"vector,omitempty"`
	Accepted    int              `json:"accepted"`
	Rejected    []SyncChange     `json:"rejected,omitempty"`
	// Devices announces verification keys the peer may not have yet.
	Devices []DeviceAnnounce `json:"devices,omitempty"`
	// Error carries a refusal (unknown device, id mismatch, store failure).
	// Without it a failure reply decodes as a successful empty round and the
	// caller retries forever with no signal on either side.
	Error string `json:"error,omitempty"`
}

// MergePolicy decides whether incoming wins over existing for same entity+field.
// PickWinner returns true if incoming should replace existing.
type MergePolicy interface {
	PickWinner(existing, incoming SyncChange) bool
}

// LWWPolicy implements per-field most-recent-write-wins with deterministic tie-breakers.
//   - incoming.HLC > existing.HLC wins (HLC=0 legacy rows fall back to UpdatedAt)
//   - tie -> UpdatedAt wins
//   - exact tie -> deviceId lex order (smaller wins)
//
// The former server-wins tie-break (IsServer) is deprecated for P2P but kept for
// back-compat: if both HLC and UpdatedAt tie and IsServer is set, it still applies
// before lex. New code should leave IsServer nil.
//
// Delete-wins is handled in SyncStore.Reconcile before delegating to PickWinner.
type LWWPolicy struct {
	IsServer func(string) bool
}

// Entity types carried by the change log. They are declared here, next to
// SyncChange, because both the emitters and the projection have to agree on
// them and neither owns the vocabulary.
const (
	// EntityCatalog replicates the catalog entity itself. Catalog ids are minted
	// locally and are not derivable from the track, so a peer that receives a
	// change keyed on trk_… has no idea what track it means until it has seen
	// the entity. Everything else keyed on a catalog id depends on this.
	EntityCatalog = "catalogEntity"
	// EntityTrack carries per-track metadata under a catalog id.
	EntityTrack = "track"
	// EntityPlaylist carries a managed playlist under its playlist id.
	EntityPlaylist = "playlist"
	// EntityPlay carries one play event under its play id.
	EntityPlay = "play"
	// EntityAlbum carries album-level metadata — a rename, an uploaded cover —
	// under a stable album key rather than a backend album id, which belongs to
	// one library backend and means nothing on another device.
	EntityAlbum = "album"
	// EntityArtist carries artist-level metadata under a stable artist key.
	EntityArtist = "artist"
)

// Field names carried by the change log. They are the wire format — renaming
// one renames it for every paired device — and both the emitters and the
// projection have to agree on them, so they live here rather than in either.
const (
	// FieldDeleted is the tombstone sentinel: the field name that means "this
	// entity is gone", rather than a value of some field.
	FieldDeleted = "__deleted"
	// FieldIdentity carries a catalog entity's metadata, on EntityCatalog.
	FieldIdentity = "identity"
	// FieldRecord carries a whole play event, on EntityPlay.
	FieldRecord = "record"

	// Per-track fields, on EntityTrack under a catalog id.
	FieldTitle          = "title"
	FieldArtist         = "artist"
	FieldCropStartMs    = "cropStartMs"
	FieldCropEndMs      = "cropEndMs"
	FieldQuality        = "quality"
	FieldLoudnessGainDb = "loudnessGainDb"
	// FieldAlbum is the album name shown for a track. It is per-track like the
	// title: renaming the album itself travels on EntityAlbum instead.
	FieldAlbum = "album"

	// FieldName is the display name of an album or artist, on EntityAlbum and
	// EntityArtist.
	FieldName = "name"
	// FieldCover is an uploaded cover, as "<sha256>.<ext>", on EntityAlbum and
	// EntityTrack. Empty means the upload was removed and the library backend's
	// own art applies again. Only the address travels in the log; the bytes are
	// fetched from the peer that has them.
	FieldCover = "cover"
)
