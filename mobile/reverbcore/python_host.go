//go:build !(ios && pyembed)

package reverbcore

import "github.com/uhhhm/reverb/internal/pyrun"

// phonePython is nil without the embedded interpreter, so the phone profile
// runs the host's Python. That is what Linux tests want; an iOS build made
// without the pyembed tag has no Python, and downloads and external playback
// fail on it.
func phonePython(_ string) (pyrun.Runner, error) { return nil, nil }
