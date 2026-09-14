package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/recommend"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
)

func TestDatabaseRequiresMatchingSourceResponses(t *testing.T) {
	err := run(context.Background(), []string{"-db", "/tmp/reverb-quality-test.db"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "-record-cache") || !strings.Contains(err.Error(), "-fixture") {
		t.Fatalf("error = %v, want instructions to capture or replay matching responses", err)
	}
}

// stalledSource never answers until its request is cancelled.
type stalledSource struct{}

func (stalledSource) Name() string { return "listenbrainz" }

func (stalledSource) SimilarTracks(ctx context.Context, _ recommend.TrackSeed, _ int) ([]recommend.TrackCandidate, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRecordingStopsAtTheRunTimeout(t *testing.T) {
	previous := newSource
	newSource = func() recommend.TrackSimilarity { return stalledSource{} }
	t.Cleanup(func() { newSource = previous })

	args := []string{
		"-db", playsDatabase(t), "-record-cache", t.TempDir() + "/recorded.json",
		"-fixture", "../../internal/recommend/quality/testdata/history.json", "-timeout", "50ms",
	}
	start := time.Now()
	err := run(context.Background(), args, io.Discard, io.Discard)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want a timeout error", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("run took %s, want it bounded by -timeout", elapsed)
	}
}

// playsDatabase holds one play on each side of the fixture's holdout.
func playsDatabase(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/reverb.db"
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	plays := []struct {
		id       string
		playedAt int64
	}{{"track-1", 1_000_000}, {"track-2", 1_000_000 + 10*24*60*60}}
	for _, p := range plays {
		id, playedAt := p.id, p.playedAt
		if err := st.Q().InsertCatalogEntity(ctx, db.InsertCatalogEntityParams{
			ID: id, Kind: "track", Title: "Track " + id, Artist: "Artist", CreatedAt: 1,
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.Q().InsertPlay(ctx, db.InsertPlayParams{
			ID: "play-" + id, UserID: "owner", CatalogID: id, PlayedAt: playedAt, CreatedAt: playedAt, Qualified: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
