package download

import (
	"log"

	"github.com/uhhhm/reverb/internal/portablename"
)

// BeginPortableSweep records what is in an adapter's output directory and
// returns the function to call once a download has succeeded, which renames
// whatever it just minted onto a name every device in the household can store.
//
// Both adapters need this and neither can do it from its output template alone:
// spotDL substitutes {artists} and {title} from Spotify's metadata, and yt-dlp's
// %(title)s fallback comes from the source. The name is only knowable once the
// file exists, and it has to be corrected before the library scan indexes it or
// a peer's manifest advertises it.
//
// The before-and-after pair is what distinguishes this download's file from the
// rest of the library, without depending on mtime — yt-dlp stamps a file with
// the source's upload date, so "modified since I started" would not identify
// it. Nothing is renamed when the snapshot could not be taken: sweeping with no
// idea what was already there would migrate the owner's whole library as a side
// effect of one download.
//
// A sweep failure is logged rather than returned. The track did download;
// failing the job over its name would cost the owner the file.
func BeginPortableSweep(dir, adapter string) func() {
	before, err := portablename.Snapshot(dir)
	if err != nil {
		log.Printf("%s: could not read %s, skipping the portable-name sweep: %v", adapter, dir, err)
		return func() {}
	}
	return func() {
		moved, err := portablename.SweepNew(dir, before)
		if err != nil {
			log.Printf("%s: portable-name sweep of %s: %v", adapter, dir, err)
		}
		for _, m := range moved {
			log.Printf("%s: renamed %q to %q so every device in the household can store it", adapter, m.From, m.To)
		}
	}
}
