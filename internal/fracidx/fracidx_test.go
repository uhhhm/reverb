package fracidx

import (
	"sort"
	"testing"
)

func TestBetweenIsStrictlyBetween(t *testing.T) {
	cases := [][2]string{{"", ""}, {"", "a"}, {"a", ""}, {"a", "b"}, {"a", "ai"}, {"zz", ""}, {"a", "b1"}}
	for _, c := range cases {
		got, err := Between(c[0], c[1])
		if err != nil {
			t.Fatalf("Between(%q,%q): %v", c[0], c[1], err)
		}
		if got <= c[0] {
			t.Errorf("Between(%q,%q) = %q, not above lower", c[0], c[1], got)
		}
		if c[1] != "" && got >= c[1] {
			t.Errorf("Between(%q,%q) = %q, not below upper", c[0], c[1], got)
		}
		if got[len(got)-1] == '0' {
			t.Errorf("Between(%q,%q) = %q ends in '0'", c[0], c[1], got)
		}
	}
}

func TestBetweenRejectsOutOfOrderBounds(t *testing.T) {
	if _, err := Between("b", "a"); err == nil {
		t.Fatal("expected an error for reversed bounds")
	}
	if _, err := Between("a", "a"); err == nil {
		t.Fatal("expected an error for equal bounds")
	}
}

// Repeatedly inserting between the same two neighbours must keep working: this
// is the case a renumbering scheme cannot handle.
func TestBetweenSurvivesRepeatedInsertion(t *testing.T) {
	lo, hi := "a", "b"
	for i := 0; i < 200; i++ {
		mid, err := Between(lo, hi)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if mid <= lo || mid >= hi {
			t.Fatalf("iteration %d: %q not between %q and %q", i, mid, lo, hi)
		}
		hi = mid
	}
}

func TestAssignOrdersAnEmptyList(t *testing.T) {
	got := Assign(map[string]string{}, []string{"a", "b", "c"})
	if len(got) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(got))
	}
	if !(got["a"] < got["b"] && got["b"] < got["c"]) {
		t.Errorf("keys out of order: %v", got)
	}
}

// Moving one item must rewrite one key. Rewriting the whole list would discard
// a concurrent move of a different item on another device.
func TestAssignOnlyRewritesWhatMoved(t *testing.T) {
	existing := map[string]string{"a": "a", "b": "b", "c": "c"}
	got := Assign(existing, []string{"a", "c", "b"})
	if len(got) != 1 {
		t.Fatalf("expected 1 rewritten key, got %v", got)
	}
	if _, ok := got["b"]; !ok {
		t.Fatalf("expected b to move, got %v", got)
	}
}

func TestAssignPlacesNewMembersInPosition(t *testing.T) {
	existing := map[string]string{"a": "a", "c": "c"}
	got := Assign(existing, []string{"a", "b", "c"})
	merged := map[string]string{"a": "a", "c": "c"}
	for k, v := range got {
		merged[k] = v
	}
	keys := []string{"a", "b", "c"}
	sort.Slice(keys, func(i, j int) bool { return merged[keys[i]] < merged[keys[j]] })
	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Fatalf("merged order = %v (keys %v)", keys, merged)
	}
}
