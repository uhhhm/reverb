package trackref

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSharedWireFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Source, ExternalID, Encoded string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := EncodeExternalID(c.Source, c.ExternalID); got != c.Encoded {
			t.Fatalf("%q != %q", got, c.Encoded)
		}
		source, id, ok := DecodeExternalID(c.Encoded)
		if !ok || source != strings.TrimSpace(c.Source) || id != strings.TrimSpace(c.ExternalID) {
			t.Fatalf("decode %q: %q %q %v", c.Encoded, source, id, ok)
		}
	}
}
