package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/player"
)

// phonePlayer drives a phone's core queue the way the iPhone app does: queue
// moves, and a playback sample about once a second of the current play.
type phonePlayer struct {
	d       *syncDevice
	session string
	state   player.State
}

func (p *phonePlayer) post(path string, body any) {
	p.d.t.Helper()
	p.d.must(http.MethodPost, "/player/"+p.session+path, body, &p.state, http.StatusOK)
}

func (p *phonePlayer) entry() string { return p.state.Entries[p.state.Index].ID }

func (p *phonePlayer) sample(atMS float64, seeking bool) {
	p.d.t.Helper()
	p.post("/progress", player.Progress{EntryID: p.entry(), PlayID: p.state.PlayID, PositionMS: atMS, Playing: true, Seeking: seeking})
}

// listen plays the current entry from fromMS to toMS, sampled each second.
func (p *phonePlayer) listen(fromMS, toMS float64) {
	p.d.t.Helper()
	for at := fromMS; at < toMS; at += 1000 {
		p.sample(at, false)
	}
	p.sample(toMS, false)
}

func (p *phonePlayer) move(path string) {
	p.d.t.Helper()
	p.post(path, map[string]string{"entryId": p.entry()})
}

func e2eTrack(id, title string, seconds int, origin string) json.RawMessage {
	t := map[string]any{"id": id, "title": title, "artist": "Tidewater", "album": "Low Season", "durationMs": seconds * 1000}
	if origin != "" {
		t["recommendationOrigin"] = origin
	}
	raw, _ := json.Marshal(t)
	return raw
}

// recordedPlay is a play as the desktop holds it after replication.
type recordedPlay struct {
	Title     string `json:"title"`
	Origin    string `json:"origin"`
	Qualified bool   `json:"qualified"`
	Completed bool   `json:"completed"`
	MsPlayed  int64  `json:"msPlayed"`
	Run       string `json:"run"`
}

func (d *syncDevice) plays() []recordedPlay {
	d.t.Helper()
	rows, err := d.rt.Store.DB().Query(`SELECT e.title, p.origin, p.qualified, p.completed, p.ms_played, p.session_id
		FROM plays p JOIN catalog_entity e ON e.id = p.catalog_id ORDER BY p.created_at, p.rowid`)
	if err != nil {
		d.t.Fatal(err)
	}
	defer rows.Close()
	var out []recordedPlay
	for rows.Next() {
		var r recordedPlay
		if err := rows.Scan(&r.Title, &r.Origin, &r.Qualified, &r.Completed, &r.MsPlayed, &r.Run); err != nil {
			d.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// A phone reports only playback samples; its core decides what was listened
// to — seeks, a short recommendation skip, repeat-one, a Radio track heard to
// the end, a new run after the queue plays out — and the history it records
// reaches the paired desktop.
func TestPhoneListeningIsDecidedByTheCoreAndReplicates(t *testing.T) {
	if testing.Short() {
		t.Skip("boots two runtimes with real libp2p hosts")
	}
	desktop := newSyncDevice(t, "desktop")
	phone := newPhoneDevice(t, "phone")
	pair(t, desktop, phone)

	notices, unsub := phone.rt.Bus.Subscribe(player.TopicListen)
	defer unsub()

	p := &phonePlayer{d: phone, session: "iphone"}
	p.post("/play", map[string]any{"start": 0, "tracks": []json.RawMessage{
		e2eTrack("t-harbor", "Harbor Lights", 200, ""),
		e2eTrack("t-drive", "Night Drive", 180, ""),
		e2eTrack("t-moons", "Paper Moons", 150, "shelf"),
		e2eTrack("t-loop", "Loop", 60, ""),
	}})

	// Harbor Lights: 40s, a seek to 150s, 15s more. 55s of 200s is no listen.
	p.listen(0, 40_000)
	p.sample(150_000, true)
	p.listen(150_000, 165_000)
	p.move("/next")

	// Night Drive: 60s, back to 10s, 30s more. The seek itself is not heard;
	// the 90s of listening are half the track.
	p.listen(0, 60_000)
	p.sample(10_000, true)
	p.listen(10_000, 40_000)
	p.move("/next")

	// Paper Moons, from a Home shelf: skipped after 12s.
	p.listen(0, 12_000)
	p.move("/next")

	// Loop, on repeat: heard through twice.
	p.post("/repeat", map[string]string{"mode": "one"})
	for range 2 {
		p.listen(0, 60_000)
		p.move("/ended")
	}
	p.post("/repeat", map[string]string{"mode": "off"})

	// A track Radio lined up, heard to its end; the queue then plays out.
	p.post("/enqueue", map[string]any{"origin": "radio", "tracks": []json.RawMessage{e2eTrack("t-bloom", "Static Bloom", 120, "")}})
	p.move("/next")
	p.listen(0, 120_000)
	p.move("/ended")
	if !p.state.Finished {
		t.Fatal("the queue did not play out")
	}

	// Playing again later is a new run.
	p.post("/play", map[string]any{"start": 0, "tracks": []json.RawMessage{e2eTrack("t-glow", "Afterglow", 100, "")}})
	p.listen(0, 55_000)

	want := []recordedPlay{
		{Title: "Night Drive", Qualified: true, MsPlayed: 90_000},
		{Title: "Paper Moons", Origin: "shelf", MsPlayed: 12_000},
		{Title: "Loop", Qualified: true, MsPlayed: 30_000},
		{Title: "Loop", Qualified: true, MsPlayed: 30_000},
		{Title: "Static Bloom", Origin: "radio", Qualified: true, Completed: true, MsPlayed: 119_000},
		{Title: "Afterglow", Qualified: true, MsPlayed: 50_000},
	}
	var got []recordedPlay
	converge(t, desktop, phone, "the phone's listening reaches the desktop", func() bool {
		got = desktop.plays()
		return len(got) >= len(want)
	})

	// Runs are random ids: name them in order of appearance.
	runs := map[string]string{}
	for i := range got {
		if _, ok := runs[got[i].Run]; !ok && got[i].Run != "" {
			runs[got[i].Run] = fmt.Sprintf("run%d", len(runs)+1)
		}
		got[i].Run = runs[got[i].Run]
	}
	for i := range want {
		want[i].Run = "run1"
	}
	want[len(want)-1].Run = "run2"
	if len(got) != len(want) {
		t.Fatalf("desktop holds %d plays, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		// A listen is recorded at the sample that qualifies it: allow for the
		// one-second sampling.
		g := got[i]
		if g.Title != want[i].Title || g.Origin != want[i].Origin || g.Qualified != want[i].Qualified ||
			g.Completed != want[i].Completed || g.Run != want[i].Run || g.MsPlayed < want[i].MsPlayed || g.MsPlayed > want[i].MsPlayed+1000 {
			t.Fatalf("play %d = %+v, want %+v\nall: %+v", i, g, want[i], got)
		}
	}
	if n := len(phone.plays()); n != len(want) {
		t.Fatalf("the phone recorded %d plays, want %d", n, len(want))
	}

	// Each recorded listen was announced for this session, so open stats refresh.
	deadline := time.After(5 * time.Second)
	for i := range want {
		select {
		case ev := <-notices:
			if e, ok := ev.Payload.(player.ListenEvent); !ok || e.Session != "iphone" {
				t.Fatalf("notice %d = %+v", i, ev.Payload)
			}
		case <-deadline:
			t.Fatalf("heard %d listen notices, want %d", i, len(want))
		}
	}

	sort.SliceStable(got, func(i, j int) bool { return got[i].Run < got[j].Run })
	writeE2EArtifact(t, "phone-listening.json", got)
}
