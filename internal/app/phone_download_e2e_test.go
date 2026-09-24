package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/offlineset"
	"github.com/uhhhm/reverb/internal/pyrun/pyruntest"
)

// These cases cover a Download made on a phone (ADR 0003): it plays there at
// once, stays pending upload until a paired device holds it, and then leaves
// the phone unless an offline playlist names it.

// stubDownloadingYtDlp downloads the way yt-dlp does as far as Reverb can see:
// it writes the file its --output template names (an MP3 tagged with the
// artist and title the template carries), and reports it through
// --print-to-file.
const stubDownloadingYtDlp = `import os, struct, sys
args = sys.argv[1:]
if "--version" in args:
    print("2026.09.01")
    sys.exit(0)
template = args[args.index("--output") + 1]
path = template.replace("%(ext)s", "mp3")
artist, title = os.path.basename(path)[:-4].split(" - ", 1)
body = b""
for frame, text in (("TIT2", title), ("TPE1", artist), ("TALB", "Record")):
    data = b"\x00" + text.encode()
    body += frame.encode() + struct.pack(">I", len(data)) + b"\x00\x00" + data
n = len(body)
tag = b"ID3\x03\x00\x00" + bytes([n >> 21 & 0x7f, n >> 14 & 0x7f, n >> 7 & 0x7f, n & 0x7f]) + body
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(path, "wb") as f:
    f.write(tag + b"\xff\xfb\x90\x00" * 1024)
print("[download] 100% of 4.00KiB")
if "--print-to-file" in args:
    i = args.index("--print-to-file")
    with open(args[i + 2], "w") as f:
        f.write(path + "\n")
`

// newDownloadingPhone is a phone whose yt-dlp downloads with the stub.
func newDownloadingPhone(t *testing.T) *syncDevice {
	t.Helper()
	py := pyruntest.Host(t, map[string]string{"yt_dlp": stubDownloadingYtDlp, "spotdl": "import sys\nsys.exit(1)\n"})
	return newPhoneDevice(t, "phone", func(d *syncDevice) { d.python = py })
}

// download has the phone download a track and waits until it is linked to
// the phone's library, returning the job.
func (d *syncDevice) download(title string) core.DownloadJob {
	d.t.Helper()
	var job core.DownloadJob
	d.must(http.MethodPost, "/downloads", map[string]any{
		"source": "deezer", "externalId": "dz-" + title, "artist": "Band", "title": title, "album": "Record",
	}, &job, http.StatusOK)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		var jobs []core.DownloadJob
		d.must(http.MethodGet, "/downloads", nil, &jobs, http.StatusOK)
		for _, j := range jobs {
			if j.ID != job.ID {
				continue
			}
			if j.Status == core.DownloadFailed {
				d.t.Fatalf("download of %q failed: %s", title, j.Error)
			}
			if j.Status == core.DownloadCompleted && j.LibraryTrackID != "" {
				return j
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	d.t.Fatalf("download of %q never reached the library", title)
	return job
}

func (d *syncDevice) pendingUploads() []offlineset.PendingUpload {
	d.t.Helper()
	var out struct {
		Files []offlineset.PendingUpload `json:"files"`
	}
	d.must(http.MethodGet, "/pending-uploads", nil, &out, http.StatusOK)
	return out.Files
}

// stream reads a library track from the device.
func (d *syncDevice) stream(trackID string) []byte {
	d.t.Helper()
	resp, err := d.srv.Client().Get(d.srv.URL + "/api/v1/stream/" + trackID)
	if err != nil {
		d.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		d.t.Fatalf("stream %s: status %d", trackID, resp.StatusCode)
	}
	return body
}

// uploadUntil runs file rounds on both devices until cond holds: the desktop
// fetches what the phone offers, and the phone learns what the desktop holds.
func uploadUntil(t *testing.T, desktop, phone *syncDevice, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		desktop.rt.P2PPuller.PullNow(context.Background())
		phone.rt.P2PPuller.PullNow(context.Background())
		if cond() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s: phone pending %+v, files %v; desktop files %v", what, phone.pendingUploads(), phone.musicFiles(), desktop.musicFiles())
}

func TestPhoneDownloadStaysUntilTheDesktopHoldsIt(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop", withBuiltInLibrary)
	phone := newDownloadingPhone(t)
	pair(t, desktop, phone)
	desktop.stop()

	// The Download plays on the phone at once.
	job := phone.download("Found")
	rel := "Band - Found.mp3"
	onDisk, err := os.ReadFile(filepath.Join(phone.musicDir, rel))
	if err != nil {
		t.Fatalf("the download is not in the phone's library folder: %v", err)
	}
	if got := phone.stream(job.LibraryTrackID); !bytes.Equal(got, onDisk) {
		t.Fatalf("streamed %d bytes, want the downloaded file", len(got))
	}

	// With the desktop gone it stays, pending upload, round after round.
	if p := phone.pendingUploads(); len(p) != 1 || p[0].RelPath != rel || p[0].Title != "Found" {
		t.Fatalf("pending uploads = %+v", p)
	}
	for i := 0; i < 3; i++ {
		phone.rt.P2PPuller.PullNow(context.Background())
	}
	if files := phone.musicFiles(); !equalStrings(files, []string{rel}) {
		t.Fatalf("phone holds %v with the desktop gone", files)
	}

	// Back on the network, the desktop fetches it; once the phone sees the
	// desktop holds it, the phone lets it go.
	desktop.boot()
	uploadUntil(t, desktop, phone, "the desktop takes the Download and the phone prunes it", func() bool {
		return len(phone.pendingUploads()) == 0 && len(phone.musicFiles()) == 0
	})
	if got, err := os.ReadFile(filepath.Join(desktop.musicDir, rel)); err != nil || !bytes.Equal(got, onDisk) {
		t.Fatalf("the desktop does not hold the Download: %v", err)
	}
}

func TestPhoneDownloadInAnOfflinePlaylistStays(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop", withBuiltInLibrary)
	phone := newDownloadingPhone(t)
	pair(t, desktop, phone)

	var pl core.SyncedPlaylistDetail
	phone.must(http.MethodPost, "/playlists", map[string]string{"name": "Commute"}, &pl, http.StatusCreated)
	phone.must(http.MethodPost, "/playlists/"+pl.ID+"/tracks", songBody("Kept"), nil, http.StatusOK)
	phone.must(http.MethodPut, "/offline-set/"+pl.ID, map[string]bool{"enabled": true}, nil, http.StatusOK)

	phone.download("Kept")
	rel := "Band - Kept.mp3"
	uploadUntil(t, desktop, phone, "the desktop takes the Download", func() bool {
		_, err := os.Stat(filepath.Join(desktop.musicDir, rel))
		return err == nil && len(phone.pendingUploads()) == 0
	})
	for i := 0; i < 3; i++ {
		phone.rt.P2PPuller.PullNow(context.Background())
	}
	if files := phone.musicFiles(); !equalStrings(files, []string{rel}) {
		t.Fatalf("phone holds %v, want the Download its offline playlist names", files)
	}
	if s := phone.offlineTrackStates(pl.ID); s["Kept"] != offlineset.StateReady {
		t.Fatalf("offline states = %v", s)
	}

	// It is the offline set's now: unmarking the playlist lets it go.
	phone.must(http.MethodPut, "/offline-set/"+pl.ID, map[string]bool{"enabled": false}, nil, http.StatusOK)
	fetchUntil(t, phone, "the file leaves with its playlist", func() bool { return len(phone.musicFiles()) == 0 })
}
