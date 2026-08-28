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
}

// SyncResponse is returned by the server after merging inbound changes.
type SyncResponse struct {
	Changes     []SyncChange     `json:"changes"`
	NewRevision int64            `json:"newRevision"`
	NewHLC      int64            `json:"newHlc,omitempty"`
	Vector      map[string]int64 `json:"vector,omitempty"`
	Accepted    int              `json:"accepted"`
	Rejected    []SyncChange     `json:"rejected,omitempty"`
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
