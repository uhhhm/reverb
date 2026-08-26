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
// Rules: incoming.UpdatedAt > existing.UpdatedAt wins; tie -> server wins, then lex.
// If the receiver's IsServer is set, it is used; otherwise the package global is
// consulted for backward compatibility with tests that set it directly.
func (p LWWPolicy) PickWinner(existing, incoming SyncChange) bool {
	if incoming.UpdatedAt != existing.UpdatedAt {
		return incoming.UpdatedAt > existing.UpdatedAt
	}
	// tie on wall clock: server wins
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
