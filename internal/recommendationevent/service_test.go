package recommendationevent_test

import (
	"context"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/recommendationevent"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/syncemit"
)

type emitter struct {
	id  string
	add syncemit.RecommendationAdd
}

func (e *emitter) EmitRecommendationAdd(_ context.Context, id string, add syncemit.RecommendationAdd) {
	e.id, e.add = id, add
}

func TestRecordPersistsAndReplicatesRecommendationAddition(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	e := &emitter{}
	svc := recommendationevent.New(st.Q(), e, func() time.Time { return time.Unix(1234, 0) }, func() string { return "add-1" })
	if err := svc.Record(context.Background(), "local", "similarTracks", recommendationevent.ActionPlaylist); err != nil {
		t.Fatal(err)
	}
	var origin, action string
	if err := st.DB().QueryRow(`SELECT origin, action FROM recommendation_add WHERE id='add-1'`).Scan(&origin, &action); err != nil {
		t.Fatal(err)
	}
	if origin != "similarTracks" || action != "playlist" || e.id != "add-1" || e.add.Origin != origin {
		t.Fatalf("stored %s/%s, emitted %q %+v", origin, action, e.id, e.add)
	}
}
