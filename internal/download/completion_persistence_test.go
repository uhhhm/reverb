package download

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/events"
)

// The displayed error may be translated or replaced independently of lifecycle.
// Real persistence and a fresh Manager must still retain and recover the output.
func TestCompletionRecoveryIgnoresDisplayedError(t *testing.T) {
	ctx := context.Background()
	s := newSQLStore(t)
	dl := &fakeDL{name: "dl", canDownload: true}
	build := func() *Manager {
		return NewManager(Config{Workers: 1, DebounceWindow: time.Hour, ReconcileEvery: time.Hour}, wrapDownloaders([]Downloader{dl}), s, events.New(), &fakeScanner{}, &fakeRematcher{}, &fakeVersion{}, RealClock{}, nil, nil)
	}
	m := build()
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error { return errors.New("recording unavailable") })
	m.Start()
	req := core.DownloadRequest{Source: "spotify", ExternalID: "e1", Artist: "A", Title: "T"}
	j, err := m.Enqueue(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waitForCompletionError(t, s, j.ID)
	m.Stop()
	j, _, _ = s.Get(ctx, j.ID)
	j.Error = "Please try recording again"
	if err := s.Update(ctx, j); err != nil {
		t.Fatal(err)
	}
	m = build()
	m.SetCompletionHook(func(context.Context, core.DownloadRequest, string) error { return errors.New("still unavailable") })
	if err := m.Cancel(ctx, j.ID); err == nil {
		t.Fatal("Cancel discarded existing output after error changed")
	}
	if err := m.Clear(ctx, j.ID); err == nil {
		t.Fatal("Clear discarded pending output")
	}
	if ids, err := m.ClearFinished(ctx); err != nil || len(ids) != 0 {
		t.Fatalf("clear: %v %v", ids, err)
	}
	m.SetCompletionHook(func(_ context.Context, _ core.DownloadRequest, path string) error {
		if path != "/out/e1.mp3" {
			t.Errorf("recovered path %q", path)
		}
		return nil
	})
	m.Start()
	defer m.Stop()
	waitForStatus(t, s, j.ID, core.DownloadCompleted)
	if dl.starts() != 1 {
		t.Fatalf("downloads=%d", dl.starts())
	}
}
