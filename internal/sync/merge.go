package sync

// deviceIsServerLookup is an optional hook for tie-break testing.
// When set, PickWinner uses it to decide server-wins. SyncStore now injects
// via LWWPolicy.IsServer instead of mutating this global; the global remains
// only for direct PickWinner tests that call SetDeviceIsServerLookup.
var deviceIsServerLookup func(deviceID string) bool

// SetDeviceIsServerLookup sets the package-level server check used by LWWPolicy.
// Pass nil to clear.
func SetDeviceIsServerLookup(fn func(string) bool) {
	deviceIsServerLookup = fn
}

// PickWinner returns true if incoming wins over existing.
// Rules (P2P HLC):
//  1. HLC (if non-zero) wins; legacy rows with HLC=0 fall back to UpdatedAt.
//  2. Tie on chosen clock -> UpdatedAt (if HLC tie) or lex if both tie.
//  3. Server-wins tie-break is deprecated but honored only if both HLC and
//     UpdatedAt tie and IsServer is configured (for back-compat with star tests).
func (p LWWPolicy) PickWinner(existing, incoming SyncChange) bool {
	// Prefer HLC only when both sides have it (P2P rows). Legacy rows with HLC=0
	// use wall time so that a legacy inbound (HLC=0, UpdatedAt=2000) can still
	// win over a P2P row (HLC=1000) when its wall is newer — the tick for the
	// legacy inbound will be assigned on append, not on comparison.
	if incoming.HLC != 0 && existing.HLC != 0 {
		if incoming.HLC != existing.HLC {
			return incoming.HLC > existing.HLC
		}
		// HLC tie -> fall through to UpdatedAt then lex/server.
		if incoming.UpdatedAt != existing.UpdatedAt {
			return incoming.UpdatedAt > existing.UpdatedAt
		}
	} else {
		if incoming.UpdatedAt != existing.UpdatedAt {
			return incoming.UpdatedAt > existing.UpdatedAt
		}
	}
	// tie on clock: server wins (deprecated) then lex
	lookup := p.IsServer
	if lookup == nil {
		lookup = deviceIsServerLookup
	}
	if lookup != nil {
		incServer := lookup(incoming.DeviceID)
		exServer := lookup(existing.DeviceID)
		if incServer != exServer {
			return incServer
		}
	}
	// exact tie: deterministic lex order (smaller wins)
	return incoming.DeviceID < existing.DeviceID
}
