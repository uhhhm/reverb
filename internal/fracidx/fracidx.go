// Package fracidx generates order keys that sort lexicographically.
//
// Two devices editing the same list independently must not have to renumber it:
// renumbering rewrites every position, so a single reorder on one device would
// collide with an unrelated reorder on the other. A fractional key is chosen
// strictly between its neighbours instead, so moving one item touches one key
// and concurrent moves of different items merge without conflict.
package fracidx

import (
	"errors"
	"strings"
)

const digits = "0123456789abcdefghijklmnopqrstuvwxyz"

// ErrOutOfOrder is returned when the bounds handed to Between are not in order.
var ErrOutOfOrder = errors.New("fracidx: lower bound is not below upper bound")

// Between returns a key that sorts strictly after lo and strictly before hi.
// An empty lo means "before every key"; an empty hi means "after every key",
// so Between("", "") is the key for the first item in an empty list.
//
// Keys never end in '0', which is what makes an arbitrarily long chain of
// insertions between the same two neighbours always possible.
func Between(lo, hi string) (string, error) {
	if hi != "" && lo >= hi {
		return "", ErrOutOfOrder
	}
	return midpoint(lo, hi), nil
}

func midpoint(lo, hi string) string {
	if hi != "" {
		n := 0
		for n < len(hi) && n < len(lo) && lo[n] == hi[n] {
			n++
		}
		if n > 0 {
			return hi[:n] + midpoint(lo[n:], hi[n:])
		}
	}
	dLo := 0
	if lo != "" {
		dLo = strings.IndexByte(digits, lo[0])
	}
	dHi := len(digits)
	if hi != "" {
		dHi = strings.IndexByte(digits, hi[0])
	}
	if dHi-dLo > 1 {
		return string(digits[(dLo+dHi)/2])
	}
	if dHi == dLo {
		// Only reachable with no lower bound and an upper bound starting at '0':
		// there is no digit below it, so descend into the upper bound instead.
		return hi[:1] + midpoint("", hi[1:])
	}
	// The two bounds are on adjacent digits, so nothing fits at this position.
	// Extend the lower bound: anything starting with its digit stays below hi.
	rest := ""
	if lo != "" {
		rest = lo[1:]
	}
	return string(digits[dLo]) + midpoint(rest, "")
}

// Assign works out the order keys needed to put members into the given order,
// given the keys they already have. It returns only the members whose key has
// to change: a reorder that moves one item must not rewrite the rest, or it
// would clobber a concurrent edit on another device for no reason.
func Assign(existing map[string]string, members []string) map[string]string {
	out := map[string]string{}
	lower := ""
	for i, m := range members {
		if cur, ok := existing[m]; ok && cur > lower {
			lower = cur
			continue
		}
		upper := ""
		for j := i + 1; j < len(members); j++ {
			if v, ok := existing[members[j]]; ok && v > lower {
				upper = v
				break
			}
		}
		next, err := Between(lower, upper)
		if err != nil {
			// Unreachable given upper was chosen above lower, but a bad key in
			// the log must not wedge the list: fall back to appending.
			next, _ = Between(lower, "")
		}
		out[m] = next
		lower = next
	}
	return out
}
