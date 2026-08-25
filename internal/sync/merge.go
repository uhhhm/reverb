package sync

// deviceIsServerLookup is an optional hook for tie-break testing.
// When set, PickWinner uses it to decide server-wins. SyncStore sets it to a
// DB-backed lookup; tests can override. If nil, server check is skipped and
// lex order decides.
var deviceIsServerLookup func(deviceID string) bool

// SetDeviceIsServerLookup sets the package-level server check used by LWWPolicy.
// Pass nil to clear.
func SetDeviceIsServerLookup(fn func(string) bool) {
	deviceIsServerLookup = fn
}

// PickWinner returns true if incoming wins over existing.
// Rules: incoming.UpdatedAt > existing.UpdatedAt wins; tie -> server wins, then lex.
func (LWWPolicy) PickWinner(existing, incoming SyncChange) bool {
	if incoming.UpdatedAt != existing.UpdatedAt {
		return incoming.UpdatedAt > existing.UpdatedAt
	}
	// tie on wall clock: server wins
	if deviceIsServerLookup != nil {
		incServer := deviceIsServerLookup(incoming.DeviceID)
		exServer := deviceIsServerLookup(existing.DeviceID)
		if incServer != exServer {
			return incServer
		}
	}
	// exact tie: deterministic lex order (smaller wins)
	return incoming.DeviceID < existing.DeviceID
}
