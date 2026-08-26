package sync

// SyncRequest is the envelope a device sends when syncing.
type SyncRequest struct {
	SinceRevision int64        `json:"sinceRevision"`
	Changes       []SyncChange `json:"changes"`
}

// SyncChange is a per-field mutation. Field=="__deleted" is the sentinel for deletion.
type SyncChange struct {
	EntityType string `json:"entityType"`
	EntityID   string `json:"entityId"`
	Field      string `json:"field"`
	Value      any    `json:"value"`
	UpdatedAt  int64  `json:"updatedAt"`
	DeviceID   string `json:"deviceId,omitempty"`
	Revision   int64  `json:"revision,omitempty"`
}

// SyncResponse is returned by the server after merging inbound changes.
type SyncResponse struct {
	Changes     []SyncChange `json:"changes"`
	NewRevision int64        `json:"newRevision"`
	Accepted    int          `json:"accepted"`
	Rejected    []SyncChange `json:"rejected,omitempty"`
}

// MergePolicy decides whether incoming wins over existing for same entity+field.
// PickWinner returns true if incoming should replace existing.
type MergePolicy interface {
	PickWinner(existing, incoming SyncChange) bool
}

// LWWPolicy implements per-field most-recent-write-wins with deterministic tie-breakers.
//   - incoming.UpdatedAt > existing.UpdatedAt wins
//   - tie -> server device wins over non-server (via device IsServer lookup when available)
//   - exact tie -> deviceId lex order (smaller wins)
//
// Delete-wins is handled in SyncStore.Reconcile before delegating to PickWinner.
type LWWPolicy struct {
	IsServer func(string) bool
}
