//go:build race

package recommend_test

// raceEnabled is set when the race detector slows everything down, so timing
// assertions do not apply.
const raceEnabled = true
