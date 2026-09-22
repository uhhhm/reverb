package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/uhhhm/reverb/mobile/reverbcore"
)

// The iOS smoke test's flow on Linux: the phone core as the app links it,
// driven over its API with the calls the app makes, against this test peer.
// The XCUITest adds only the Swift layer on top.
func TestAppFlowAgainstTestPeer(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	ctx, cancel := context.WithCancel(context.Background())
	peerAddr := make(chan string, 1)
	served := make(chan error, 1)
	go func() {
		served <- run(ctx, "127.0.0.1:0", 0, t.TempDir(), func(addr, _ string) { peerAddr <- addr })
	}()
	t.Cleanup(func() {
		cancel()
		<-served
	})
	var peer string
	select {
	case peer = <-peerAddr:
	case err := <-served:
		t.Fatalf("test peer: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("test peer did not start")
	}

	t.Setenv("REVERB_P2P_PORT", "0")
	port, err := reverbcore.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reverbcore.Stop)
	phone := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)

	var pairing struct{ Code, Address, Playlist, Track string }
	do(t, http.MethodPost, "http://"+peer+"/testpeer/pairing", nil, &pairing, http.StatusOK)
	do(t, http.MethodPost, phone+"/p2p/pair/redeem", map[string]string{
		"peerId": pairing.Address, "code": pairing.Code, "deviceName": "iPhone",
	}, nil, http.StatusOK)
	do(t, http.MethodPost, phone+"/sync/trigger", nil, nil, http.StatusAccepted)

	var devices []struct {
		ThisDevice bool `json:"thisDevice"`
	}
	do(t, http.MethodGet, phone+"/pairing/devices", nil, &devices, http.StatusOK)
	others := 0
	for _, d := range devices {
		if !d.ThisDevice {
			others++
		}
	}
	if others != 1 {
		t.Fatalf("the phone lists %d paired devices besides itself, want the test peer alone: %+v", others, devices)
	}

	var playlistID string
	eventually(t, "the test peer's playlist reaches the phone", func() bool {
		var lists []struct{ ID, Name string }
		do(t, http.MethodGet, phone+"/playlists", nil, &lists, http.StatusOK)
		for _, l := range lists {
			if l.Name == pairing.Playlist {
				playlistID = l.ID
			}
		}
		return playlistID != ""
	})
	do(t, http.MethodPut, phone+"/offline-set/"+playlistID, map[string]bool{"enabled": true}, nil, http.StatusOK)
	eventually(t, "the track is kept on the phone", func() bool {
		var st struct {
			Playlists []struct {
				Tracks []struct{ Title, State string }
			}
		}
		do(t, http.MethodGet, phone+"/offline-set/status", nil, &st, http.StatusOK)
		return len(st.Playlists) == 1 && len(st.Playlists[0].Tracks) == 1 && st.Playlists[0].Tracks[0].State == "ready"
	})

	var track map[string]any
	eventually(t, "the phone's library plays the kept track", func() bool {
		var det struct {
			Tracks []struct {
				LibraryTrack map[string]any `json:"libraryTrack"`
			}
		}
		do(t, http.MethodGet, phone+"/playlists/"+playlistID, nil, &det, http.StatusOK)
		if len(det.Tracks) == 1 && det.Tracks[0].LibraryTrack != nil {
			track = det.Tracks[0].LibraryTrack
		}
		return track != nil
	})
	var queue struct {
		Index   int `json:"index"`
		Entries []struct {
			Track struct{ ID, Title string }
		}
	}
	do(t, http.MethodPost, phone+"/player/phone/play", map[string]any{"tracks": []any{track}, "start": 0}, &queue, http.StatusOK)
	if queue.Index != 0 || len(queue.Entries) != 1 || queue.Entries[0].Track.Title != pairing.Track {
		t.Fatalf("queue = %+v", queue)
	}
	resp, err := http.Get(phone + "/stream/" + queue.Entries[0].Track.ID)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	_, _ = io.ReadFull(resp.Body, head)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(head) != "RIFF" {
		t.Fatalf("stream: %d %q, want the WAV file", resp.StatusCode, head)
	}

	// With the peer stopped, the phone still streams the track.
	do(t, http.MethodPost, "http://"+peer+"/testpeer/stop", nil, nil, http.StatusAccepted)
	select {
	case <-served:
		served <- nil
	case <-time.After(30 * time.Second):
		t.Fatal("the test peer did not stop")
	}
	resp, err = http.Get(phone + "/stream/" + queue.Entries[0].Track.ID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream with the peer gone: %d", resp.StatusCode)
	}
}

func do(t *testing.T, method, url string, body, out any, want int) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d %s", method, url, resp.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode %s: %v", method, url, raw, err)
		}
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
