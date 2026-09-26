package app

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/uhhhm/reverb/internal/core"
)

// A previous-minor phone speaks only the retained wire versions. Pair and
// replicate actual edits, then remove every supported sync protocol and prove
// the owner sees the incompatible device instead of a silent no-peers state.
func TestOlderPhoneProtocolWindow(t *testing.T) {
	desktop := newSyncDevice(t, "desktop")
	phone := newPhoneDevice(t, "older-phone")
	for _, family := range []string{"sync", "file", "cover", "delegated", "manifest"} {
		phone.rt.P2P.LibHost().RemoveStreamHandler(protocol.ID("/reverb/" + family + "/1.1.0"))
	}
	phone.rt.P2P.LibHost().RemoveStreamHandler("/reverb/pair/2.1.0")
	pair(t, phone, desktop)
	var playlist core.SyncedPlaylist
	phone.must(http.MethodPost, "/playlists", map[string]any{"name": "Older phone edit"}, &playlist, http.StatusCreated)
	converge(t, desktop, phone, "older phone playlist", func() bool { p, ok := desktop.playlist(playlist.ID); return ok && p.Name == "Older phone edit" })
	type info struct {
		SupportWindow int `json:"supportWindow"`
		Peers         []struct {
			DeviceID      string `json:"deviceId"`
			Compatibility string `json:"compatibility"`
			Message       string `json:"message"`
		} `json:"peers"`
	}
	read := func() info { var v info; desktop.must(http.MethodGet, "/version", nil, &v, http.StatusOK); return v }
	v := read()
	if len(v.Peers) != 1 || v.Peers[0].Compatibility != "compatible" {
		t.Fatalf("older phone status: %+v", v)
	}
	// An older phone also dials with only its own versions: the desktop must
	// answer every family's oldest version inside the window.
	desktopID := desktop.rt.P2P.LibHost().ID()
	for _, old := range []protocol.ID{"/reverb/pair/2.0.0", "/reverb/sync/1.0.0", "/reverb/file/1.0.0", "/reverb/cover/1.0.0", "/reverb/delegated/1.0.0", "/reverb/manifest/1.0.0"} {
		s, err := phone.rt.P2P.LibHost().NewStream(t.Context(), desktopID, old)
		if err != nil {
			t.Fatalf("desktop refused %s from an older phone: %v", old, err)
		}
		_ = s.Reset()
	}
	phone.rt.P2P.LibHost().RemoveStreamHandler("/reverb/sync/1.0.0")
	phone.rt.P2P.LibHost().SetStreamHandler("/reverb/sync/9.0.0", func(s network.Stream) { _ = s.Close() })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		v = read()
		if len(v.Peers) == 1 && v.Peers[0].Compatibility == "incompatible" && v.Peers[0].Message != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(v.Peers) != 1 || v.Peers[0].Compatibility != "incompatible" || !strings.Contains(v.Peers[0].Message, "Update Reverb on") {
		t.Fatalf("outside window hidden or unexplained: %+v", v)
	}
	if path := os.Getenv("REVERB_COMPAT_E2E_REPORT"); path != "" {
		b, _ := json.MarshalIndent(map[string]any{"olderPhoneSynced": true, "outsideWindow": v}, "", "  ")
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
