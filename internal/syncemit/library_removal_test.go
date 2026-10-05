package syncemit

import (
	"context"
	"errors"
	"testing"

	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

type memoryLog struct {
	changes []reverbsync.SyncChange
	fail    error
}

func (*memoryLog) ListLatestForEntity(context.Context, string, string) ([]reverbsync.SyncChange, error) {
	return nil, nil
}

func (l *memoryLog) AppendChange(_ context.Context, _ string, ch reverbsync.SyncChange) (int64, error) {
	if l.fail != nil {
		return 0, l.fail
	}
	l.changes = append(l.changes, ch)
	return int64(len(l.changes)), nil
}

func device(id string) DeviceResolver { return func(context.Context) string { return id } }

// Ways a library removal can go wrong: it is published without a log or an
// identity and the caller deletes the file anyway as if peers will hear of it;
// the membership marker is withdrawn although the file tombstone was not
// written; a track with no catalog id gets a marker under an empty id; or the
// removal writes a track tombstone, which would hide the track for good.
func TestEmitLibraryRemoval(t *testing.T) {
	ctx := context.Background()

	if err := New(nil, nil, device("dev")).EmitLibraryRemoval(ctx, "hash", "trk"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("without a log: %v, want ErrUnavailable", err)
	}

	log := &memoryLog{}
	if err := New(log, nil, device("")).EmitLibraryRemoval(ctx, "hash", "trk"); !errors.Is(err, ErrNoIdentity) || len(log.changes) != 0 {
		t.Fatalf("without an identity: %v, wrote %+v", err, log.changes)
	}

	failing := &memoryLog{fail: errors.New("disk full")}
	if err := New(failing, nil, device("dev")).EmitLibraryRemoval(ctx, "hash", "trk"); err == nil || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNoIdentity) {
		t.Fatalf("failed append: %v, want the append error", err)
	}

	log = &memoryLog{}
	if err := New(log, nil, device("dev")).EmitLibraryRemoval(ctx, "hash", ""); err != nil || len(log.changes) != 1 {
		t.Fatalf("no catalog id: %v, wrote %+v", err, log.changes)
	}

	log = &memoryLog{}
	if err := New(log, nil, device("dev")).EmitLibraryRemoval(ctx, "hash", "trk"); err != nil {
		t.Fatal(err)
	}
	want := []reverbsync.SyncChange{
		{EntityType: reverbsync.EntityFile, EntityID: "hash", Field: reverbsync.FieldDeleted},
		{EntityType: reverbsync.EntityTrack, EntityID: "trk", Field: reverbsync.FieldLibraryPresent, Value: false},
	}
	if len(log.changes) != len(want) {
		t.Fatalf("wrote %+v, want %+v", log.changes, want)
	}
	for i, ch := range log.changes {
		if ch.EntityType != want[i].EntityType || ch.EntityID != want[i].EntityID || ch.Field != want[i].Field || ch.Value != want[i].Value || ch.UpdatedAt == 0 {
			t.Fatalf("change %d is %+v, want %+v", i, ch, want[i])
		}
	}
}
