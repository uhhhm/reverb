package api

import (
	"os"
	"strings"
	"testing"
)

// Reverb has two composition roots — the server (cmd/reverb) and the desktop app
// (desktop) — that each build their own api.Deps. A dependency added to one and
// forgotten in the other produces a feature that silently does nothing in the
// other build, with no compile error to catch it: ExternalStream was wired in
// cmd/reverb only, so on desktop every play of an un-owned track returned 503.
//
// This asserts both roots mention the optional Deps fields that gate a user-
// visible feature. It is a source check, not a behavioural one, because
// constructing either root needs a real DB, adapters, and a bundle.
func TestBothCompositionRootsWireOptionalDeps(t *testing.T) {
	roots := map[string]string{
		"cmd/reverb/main.go": "../../cmd/reverb/main.go",
		"desktop/main.go":    "../../desktop/main.go",
	}
	fields := []string{"ExternalStream", "SearchAggregator", "Downloads", "Resolver"}

	for name, path := range roots {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, field := range fields {
			if !strings.Contains(string(src), field) {
				t.Errorf("%s never sets api.Deps.%s — the feature it gates is dead in that build", name, field)
			}
		}
	}
}
