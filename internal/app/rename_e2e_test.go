package app

// Rerun: go test ./internal/app -run TestLibraryRenameOverHTTPReplicatesToPeer -v
// The artifact lands in $REVERB_E2E_ARTIFACTS (or the test's temp dir) as
// library-rename-replication.json.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// trackEditFields are the per-track fields a rename or crop writes.
var trackEditFields = map[string]bool{
	reverbsync.FieldTitle: true, reverbsync.FieldArtist: true, reverbsync.FieldAlbum: true,
	reverbsync.FieldCropStartMs: true, reverbsync.FieldCropEndMs: true,
}

// withPeerDesktop boots a built-in desktop that downloads nothing itself: its
// library is what file sync copies into its folder, addressed by its own
// Navidrome under ids no other device uses.
func withPeerDesktop(d *syncDevice) {
	d.builtIn = true
	d.backend = func(t *testing.T, musicDir string) *httptest.Server {
		return folderSubsonicIDs(t, musicDir, "b-")
	}
}

// trackView is one track as a device's HTTP API shows it.
type trackView struct {
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	CropStartMs int    `json:"cropStartMs,omitempty"`
	CropEndMs   int    `json:"cropEndMs,omitempty"`
}

// catalogRow is the device's household-browsing row for a catalog id.
func (d *syncDevice) catalogRow(catalogID string) (core.CatalogLibraryTrack, bool) {
	d.t.Helper()
	var rows []core.CatalogLibraryTrack
	d.must(http.MethodGet, "/library/catalog/tracks", nil, &rows, http.StatusOK)
	for _, r := range rows {
		if r.ID == catalogID {
			return r, true
		}
	}
	return core.CatalogLibraryTrack{}, false
}

// librarySongs is the device's own library listing, by backend id.
func (d *syncDevice) librarySongs() map[string]core.Track {
	d.t.Helper()
	var songs []core.Track
	d.must(http.MethodGet, "/library/songs?size=100", nil, &songs, http.StatusOK)
	out := make(map[string]core.Track, len(songs))
	for _, s := range songs {
		out[s.ID] = s
	}
	return out
}

// views is what both of the device's track responses show for a catalog id:
// the household row, and the library song its own binding resolves to.
func (d *syncDevice) views(catalogID string) (catalog, library trackView, ok bool) {
	row, found := d.catalogRow(catalogID)
	if !found || row.LocalTrackID == "" {
		return trackView{}, trackView{}, false
	}
	song, found := d.librarySongs()[row.LocalTrackID]
	if !found {
		return trackView{}, trackView{}, false
	}
	return trackView{row.Title, row.Artist, row.Album, row.CropStartMs, row.CropEndMs},
		trackView{song.Title, song.Artist, song.Album, song.CropStartMs, song.CropEndMs}, true
}

// deviceID is the device's own identity in the change log.
func (d *syncDevice) deviceID() string {
	d.t.Helper()
	id, err := d.rt.Deps.SyncStore.LocalDeviceID(context.Background())
	if err != nil || id == "" {
		d.t.Fatalf("%s: no device id: %v", d.name, err)
	}
	return id
}

// trackEdits lists the rename and crop changes the device's log holds for
// the given catalog ids.
func (d *syncDevice) trackEdits(catalogIDs ...string) []reverbsync.SyncChange {
	d.t.Helper()
	changes, err := d.rt.Deps.SyncStore.ListSince(context.Background(), 0, 100_000)
	if err != nil {
		d.t.Fatal(err)
	}
	var out []reverbsync.SyncChange
	for _, ch := range changes {
		if ch.EntityType == reverbsync.EntityTrack && trackEditFields[ch.Field] && contains(catalogIDs, ch.EntityID) {
			out = append(out, ch)
		}
	}
	return out
}

// authoredEdits is trackEdits narrowed to what the device itself wrote.
func (d *syncDevice) authoredEdits(catalogIDs ...string) []string {
	d.t.Helper()
	self := d.deviceID()
	var out []string
	for _, ch := range d.trackEdits(catalogIDs...) {
		if ch.DeviceID == self {
			out = append(out, fmt.Sprintf("%s/%d %s=%v", ch.EntityID, ch.Seq, ch.Field, ch.Value))
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// A rename made through one desktop's HTTP API reaches its peer through the
// catalog id alone, and the peer's own later edits come back. Concurrent edits
// to different fields of one track keep both, field by field: a rename sends
// the fields it changed, not the ones it left alone.
func TestLibraryRenameOverHTTPReplicatesToPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	a := newSyncDevice(t, "alpha", withDownloadingDesktop)
	b := newSyncDevice(t, "beta", withPeerDesktop)
	pair(t, a, b)

	first := a.download("First")
	second := a.download("Second")
	ids := []string{first.CanonicalID, second.CanonicalID}

	// Beta holds the files only through file sync, and binds them to its own
	// library: a backend id of alpha's means nothing there.
	converge(t, a, b, "beta binds both of alpha's downloads to its own library", func() bool {
		b.rt.P2PPuller.PullNow(context.Background())
		for _, id := range ids {
			row, ok := b.catalogRow(id)
			if !ok || !strings.HasPrefix(row.LocalTrackID, "b-") {
				return false
			}
		}
		return true
	})
	for id := range b.librarySongs() {
		if !strings.HasPrefix(id, "b-") {
			t.Fatalf("beta's library lists %s, which is not its own backend's id", id)
		}
	}
	firstOnB, _ := b.catalogRow(first.CanonicalID)
	secondOnB, _ := b.catalogRow(second.CanonicalID)

	// --- alpha renames one track and crops the other ---
	a.must(http.MethodPut, "/library/track/"+first.LibraryTrackID+"/name", map[string]string{
		"title": "First (A)", "artist": "The Band", "album": "Record (A)",
	}, nil, http.StatusOK)
	a.must(http.MethodPut, "/library/track/"+second.LibraryTrackID+"/crop", map[string]int{
		"startMs": 15_000, "endMs": 90_000,
	}, nil, http.StatusOK)
	alphaAuthored := a.authoredEdits(ids...)
	betaAuthored := b.authoredEdits(ids...)

	renamedByA := trackView{Title: "First (A)", Artist: "The Band", Album: "Record (A)"}
	cropped := trackView{Title: "Second", Artist: "Band", Album: "Record", CropStartMs: 15_000, CropEndMs: 90_000}
	converge(t, a, b, "alpha's rename and crop show on beta", func() bool {
		c1, l1, ok1 := b.views(first.CanonicalID)
		c2, l2, ok2 := b.views(second.CanonicalID)
		return ok1 && ok2 && c1 == renamedByA && l1 == renamedByA && c2 == cropped && l2 == cropped
	})
	var crop struct{ StartMs, EndMs int }
	b.must(http.MethodGet, "/library/track/"+secondOnB.LocalTrackID+"/crop", nil, &crop, http.StatusOK)
	if crop.StartMs != 15_000 || crop.EndMs != 90_000 {
		t.Fatalf("beta's crop for its own %s is %+v", secondOnB.LocalTrackID, crop)
	}
	assertNoEcho(t, a, alphaAuthored, ids)
	assertNoEcho(t, b, betaAuthored, ids)

	// --- beta renames the track again, through its own binding; before that
	// reaches alpha, alpha changes a different field of it ---
	b.must(http.MethodPut, "/library/track/"+firstOnB.LocalTrackID+"/name", map[string]string{
		"title": "First (B)", "album": "Record (B)",
	}, nil, http.StatusOK)
	time.Sleep(20 * time.Millisecond)
	a.must(http.MethodPut, "/library/track/"+first.LibraryTrackID+"/name", map[string]string{
		"artist": "Band & Friends",
	}, nil, http.StatusOK)
	alphaAuthored = a.authoredEdits(ids...)
	betaAuthored = b.authoredEdits(ids...)

	final := trackView{Title: "First (B)", Artist: "Band & Friends", Album: "Record (B)"}
	last := map[string][2]trackView{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last views of the first track (catalog, library): %+v", last)
		}
	})
	converge(t, a, b, "both devices show each field's later edit", func() bool {
		for _, d := range []*syncDevice{a, b} {
			c1, l1, ok1 := d.views(first.CanonicalID)
			c2, l2, ok2 := d.views(second.CanonicalID)
			last[d.name] = [2]trackView{c1, l1}
			if !ok1 || !ok2 || c1 != final || l1 != final || c2 != cropped || l2 != cropped {
				return false
			}
		}
		return true
	})
	assertNoEcho(t, a, alphaAuthored, ids)
	assertNoEcho(t, b, betaAuthored, ids)

	// --- the artifact: both devices' final views and the edits exchanged,
	// with random ids replaced by stable labels so runs can be diffed ---
	labels := map[string]string{
		first.CanonicalID: "first", second.CanonicalID: "second",
		a.deviceID(): a.name, b.deviceID(): b.name,
	}
	type device struct {
		Catalog map[string]trackView `json:"catalog"`
		Library map[string]trackView `json:"library"`
	}
	type change struct {
		Author string `json:"author"`
		Seq    int64  `json:"seq"`
		Track  string `json:"track"`
		Field  string `json:"field"`
		Value  any    `json:"value"`
	}
	artifact := struct {
		Devices map[string]device `json:"devices"`
		Changes []change          `json:"changes"`
	}{Devices: map[string]device{}}
	for _, d := range []*syncDevice{a, b} {
		dev := device{Catalog: map[string]trackView{}, Library: map[string]trackView{}}
		for _, id := range ids {
			c, l, _ := d.views(id)
			dev.Catalog[labels[id]], dev.Library[labels[id]] = c, l
		}
		artifact.Devices[d.name] = dev
	}
	onA, onB := a.trackEdits(ids...), b.trackEdits(ids...)
	if len(onA) != len(onB) {
		t.Fatalf("logs hold different edits: %s %d, %s %d", a.name, len(onA), b.name, len(onB))
	}
	for _, ch := range onA {
		artifact.Changes = append(artifact.Changes, change{labels[ch.DeviceID], ch.Seq, labels[ch.EntityID], ch.Field, ch.Value})
	}
	sort.Slice(artifact.Changes, func(i, j int) bool {
		x, y := artifact.Changes[i], artifact.Changes[j]
		if x.Author != y.Author {
			return x.Author < y.Author
		}
		return x.Seq < y.Seq
	})
	writeE2EArtifact(t, "library-rename-replication.json", artifact)
}

// assertNoEcho fails if the device wrote a rename or crop change since before
// was taken: everything it has received since was applied, not republished.
func assertNoEcho(t *testing.T, d *syncDevice, before []string, ids []string) {
	t.Helper()
	if now := d.authoredEdits(ids...); !equalStrings(now, before) {
		t.Fatalf("%s echoed edits it received: before %v, now %v", d.name, before, now)
	}
}
