package download

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/linkadd"
	"github.com/uhhhm/reverb/internal/linkresolve"
	"github.com/uhhhm/reverb/internal/store"
)

// chapterSource is the linkadd downloader the planner reads chapters from.
type chapterSource struct{ chapters []core.Chapter }

func (c chapterSource) Enqueue(context.Context, core.DownloadRequest) (core.DownloadJob, error) {
	return core.DownloadJob{}, fmt.Errorf("planning only")
}

func (c chapterSource) ListChapters(context.Context, string) ([]core.Chapter, error) {
	return c.chapters, nil
}

// titleRematcher links every job to a library track named after its title and
// records what each match was asked, so a test can see the external key.
type titleRematcher struct {
	mu    sync.Mutex
	calls []core.ExternalResult
}

func (r *titleRematcher) Match(_ context.Context, ext core.ExternalResult) (core.MatchResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, ext)
	r.mu.Unlock()
	return core.MatchResult{
		Status: core.MatchInLibrary, LibraryTrackID: "lib-" + ext.Title + fmt.Sprint(ext.DurationMs),
		Method: core.MatchFuzzy, Confidence: 0.9,
	}, nil
}

// A chapter split or a trimmed range is a different recording from the whole
// video, even though every request keeps the video's source and external id.
// Linking them must not fold them into the video's catalog entity (or each
// other's), or they share play history and covers.
func TestSectionedDownloadsGetTheirOwnCatalogIdentity(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/sections.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	var n int
	cat := catalog.NewService(st.Q(), time.Now, func() string { n++; return fmt.Sprintf("%04d", n) })
	jobs := NewSQLStore(st.Q())

	res := &linkresolve.ResolveResult{
		Source: "youtube", Kind: "track", ExternalID: "vid123",
		URL: "https://www.youtube.com/watch?v=vid123", Title: "Live at the Hall", Artist: "The Band",
	}
	base := core.DownloadRequest{
		Source: res.Source, ExternalID: res.ExternalID, Title: res.Title, Artist: res.Artist,
		ManualURL: res.URL, PreferDownloader: "ytdlp",
	}
	planner := linkadd.New(nil, chapterSource{chapters: []core.Chapter{
		{Title: "Opening", StartSec: 0, EndSec: 241.5},
		{Title: "Encore", StartSec: 241.5, EndSec: 530},
	}})
	chapters, err := planner.Plan(ctx, base, res, linkadd.AddOptions{SplitChapters: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 2 {
		t.Fatalf("planned %d chapter requests, want 2", len(chapters))
	}
	trims, err := planner.Plan(ctx, base, res, linkadd.AddOptions{StartTime: "1:00", EndTime: "2:30"})
	if err != nil {
		t.Fatal(err)
	}
	whole, err := planner.Plan(ctx, base, res, linkadd.AddOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// The whole video is downloaded and linked first, so its external and
	// norm aliases already exist when the sections link.
	reqs := map[string]core.DownloadRequest{
		"whole": whole[0], "opening": chapters[0], "encore": chapters[1], "trim": trims[0],
	}
	for i, id := range []string{"whole", "opening", "encore", "trim"} {
		req := reqs[id]
		job := core.DownloadJob{
			CreatedAt: int64(4 - i), // listed newest first
			ID:        id, DedupKey: DedupKey(req), Status: core.DownloadCompleted, DownloaderName: "ytdlp",
			Source: req.Source, ExternalID: req.ExternalID, Title: req.Title, Artist: req.Artist,
			Album: req.Album, DurationMs: req.DurationMs,
		}
		if err := jobs.Insert(ctx, job, req); err != nil {
			t.Fatal(err)
		}
	}

	rematch := &titleRematcher{}
	m := NewManager(
		Config{Workers: 1, DebounceWindow: time.Millisecond},
		wrapDownloaders(nil), jobs, nil, &fakeScanner{}, rematch, &fakeVersion{v: 1}, nil, nil, nil,
	)
	m.SetCanonicalMinter(cat)
	linkID, err := cat.CanonicalFor(ctx, catalog.Identity{
		Kind: res.Kind, Source: res.Source, ExternalID: res.ExternalID,
		Title: res.Title, Artist: res.Artist,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.BackfillUnlinked()

	ids := map[string]string{}
	for _, id := range []string{"whole", "opening", "encore", "trim"} {
		j, _, err := jobs.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.CanonicalID == "" {
			t.Fatalf("job %s linked without a catalog id", id)
		}
		ids[id] = j.CanonicalID
	}
	if ids["whole"] != linkID {
		t.Errorf("whole download catalog id = %q, want link entry %q", ids["whole"], linkID)
	}
	seen := map[string]string{}
	for _, id := range []string{"whole", "opening", "encore", "trim"} {
		if other, dup := seen[ids[id]]; dup {
			t.Errorf("%s shares catalog id %s with %s", id, ids[id], other)
		}
		seen[ids[id]] = id
	}

	// The whole video is still reachable by its source address; no section is.
	byExternal, found, err := cat.Lookup(ctx, catalog.Identity{Kind: "track", Source: "youtube", ExternalID: "vid123"})
	if err != nil || !found || byExternal != ids["whole"] {
		t.Errorf("youtube:vid123 resolves to %q (found=%v, err=%v), want the whole video %q", byExternal, found, err, ids["whole"])
	}

	// A section's match must not use the video's match-cache key, or the first
	// chapter linked would answer for every other chapter and the video.
	for _, call := range rematch.calls {
		if call.Title != "Live at the Hall" || call.DurationMs != 0 {
			if call.Source != "" || call.ExternalID != "" {
				t.Errorf("section %q matched with external key %s:%s", call.Title, call.Source, call.ExternalID)
			}
		}
	}

	// The trim is the video's own title, told apart by its length.
	trimEntity, err := st.Q().GetCatalogEntity(ctx, ids["trim"])
	if err != nil {
		t.Fatal(err)
	}
	if trimEntity.DurationMs != 90000 {
		t.Errorf("trim entity duration = %dms, want 90000 (1:00 to 2:30)", trimEntity.DurationMs)
	}
	if trimEntity.Source != "" || trimEntity.ExternalID != "" {
		t.Errorf("trim entity carries the video's address %s:%s", trimEntity.Source, trimEntity.ExternalID)
	}
}
