package scrobble

import (
	"context"
	"errors"
	"testing"

	"github.com/uhhhm/reverb/internal/store/db"
)

// fakeTokenProvider is a provider linked by pasting a user token.
type fakeTokenProvider struct {
	fakeScrobbler
	username    string
	validateErr error
	validated   []string
}

func (f *fakeTokenProvider) ValidateToken(_ context.Context, token string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validated = append(f.validated, token)
	return f.username, f.validateErr
}

func newProviderService(t *testing.T) (*db.Queries, *Service, *fakeScrobbler, *fakeTokenProvider) {
	t.Helper()
	q := openTestDB(t)
	lastfm := &fakeScrobbler{}
	lb := &fakeTokenProvider{username: "lbuser"}
	svc := newTestService(t, q, lastfm, nil)
	svc.WithTokenProvider("listenbrainz", lb)
	return q, svc, lastfm, lb
}

func pendingRows(t *testing.T, q *db.Queries) []db.ScrobbleQueue {
	t.Helper()
	rows, err := q.SelectDueScrobbles(context.Background(), db.SelectDueScrobblesParams{NextAttemptAt: 999_999_999_999, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestEnqueue_OneRowPerActiveProvider(t *testing.T) {
	q, svc, _, _ := newProviderService(t)
	insertLink(t, q, "u1", "lastfm", "sk", "alice", "active")
	insertLink(t, q, "u1", "listenbrainz", "lb-token", "lbuser", "active")

	if err := svc.Enqueue(context.Background(), "u1", ScrobblePlay{Track: Track{Title: "T", Artist: "A"}, PlayedAt: 900_000}); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, r := range pendingRows(t, q) {
		got[r.Provider]++
	}
	if got["lastfm"] != 1 || got["listenbrainz"] != 1 || len(got) != 2 {
		t.Fatalf("queued per provider = %v, want one each", got)
	}
}

func TestEnqueue_SkipsLinksWithoutARegisteredProvider(t *testing.T) {
	q, svc, _, _ := newProviderService(t)
	insertLink(t, q, "u1", "mystery", "x", "x", "active")

	if err := svc.Enqueue(context.Background(), "u1", ScrobblePlay{Track: Track{Title: "T", Artist: "A"}, PlayedAt: 900_000}); err != nil {
		t.Fatal(err)
	}
	if rows := pendingRows(t, q); len(rows) != 0 {
		t.Fatalf("queued %d rows for an unknown provider", len(rows))
	}
}

func TestDrainOnce_RoutesRowsToTheirProvider(t *testing.T) {
	q, svc, lastfm, lb := newProviderService(t)
	ctx := context.Background()
	insertLink(t, q, "u1", "lastfm", "sk", "alice", "active")
	insertLink(t, q, "u1", "listenbrainz", "lb-token", "lbuser", "active")
	if err := svc.Enqueue(ctx, "u1", ScrobblePlay{Track: Track{Title: "T", Artist: "A"}, PlayedAt: 900_000}); err != nil {
		t.Fatal(err)
	}

	if err := svc.drainOnce(ctx, 50); err != nil {
		t.Fatal(err)
	}
	if len(lastfm.scrobbleCalls) != 1 || lastfm.scrobbleCalls[0].Creds.SessionKey != "sk" {
		t.Fatalf("lastfm calls = %+v", lastfm.scrobbleCalls)
	}
	if len(lb.scrobbleCalls) != 1 || lb.scrobbleCalls[0].Creds.SessionKey != "lb-token" || len(lb.scrobbleCalls[0].Plays) != 1 {
		t.Fatalf("listenbrainz calls = %+v", lb.scrobbleCalls)
	}
	if rows := pendingRows(t, q); len(rows) != 0 {
		t.Fatalf("%d rows still pending after both providers accepted", len(rows))
	}
}

func TestDrainOnce_AuthFailureBreaksOnlyThatProvider(t *testing.T) {
	q, svc, _, lb := newProviderService(t)
	ctx := context.Background()
	lb.scrobbleErr = ErrAuth
	insertLink(t, q, "u1", "lastfm", "sk", "alice", "active")
	insertLink(t, q, "u1", "listenbrainz", "lb-token", "lbuser", "active")
	if err := svc.Enqueue(ctx, "u1", ScrobblePlay{Track: Track{Title: "T", Artist: "A"}, PlayedAt: 900_000}); err != nil {
		t.Fatal(err)
	}

	_ = svc.drainOnce(ctx, 50)
	links, err := svc.Links(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, l := range links {
		status[l.Provider] = l.Status
	}
	if status["lastfm"] != "active" || status["listenbrainz"] != "broken" {
		t.Fatalf("link status = %v", status)
	}
}

func TestNowPlaying_ReachesEveryActiveProvider(t *testing.T) {
	q, svc, lastfm, lb := newProviderService(t)
	insertLink(t, q, "u1", "lastfm", "sk", "alice", "active")
	insertLink(t, q, "u1", "listenbrainz", "lb-token", "lbuser", "active")

	svc.NowPlaying(context.Background(), "u1", Track{Title: "T", Artist: "A"})
	if len(lastfm.nowPlayingCalls) != 1 || len(lb.nowPlayingCalls) != 1 || lb.nowPlayingCalls[0].Creds.SessionKey != "lb-token" {
		t.Fatalf("now playing: lastfm %+v, listenbrainz %+v", lastfm.nowPlayingCalls, lb.nowPlayingCalls)
	}
}

func TestConnectToken_StoresActiveLink(t *testing.T) {
	q, svc, _, lb := newProviderService(t)
	ctx := context.Background()
	var linked []string
	svc.OnLinkChange(func(_ context.Context, userID, provider string, active bool) {
		if active {
			linked = append(linked, userID+"/"+provider)
		}
	})

	username, err := svc.ConnectToken(ctx, "u1", "listenbrainz", "  lb-token \n")
	if err != nil {
		t.Fatal(err)
	}
	if username != "lbuser" || len(lb.validated) != 1 || lb.validated[0] != "lb-token" {
		t.Fatalf("username %q, validated %v", username, lb.validated)
	}
	link, err := q.GetScrobbleLink(ctx, db.GetScrobbleLinkParams{UserID: "u1", Provider: "listenbrainz"})
	if err != nil {
		t.Fatal(err)
	}
	if link.SessionKey != "lb-token" || link.Status != "active" || link.Username != "lbuser" {
		t.Fatalf("stored link = %+v", link)
	}
	if len(linked) != 1 || linked[0] != "u1/listenbrainz" {
		t.Fatalf("link hooks = %v", linked)
	}
}

func TestConnectToken_RejectedTokenStoresNothing(t *testing.T) {
	q, svc, _, lb := newProviderService(t)
	ctx := context.Background()
	lb.validateErr = ErrAuth

	if _, err := svc.ConnectToken(ctx, "u1", "listenbrainz", "bad"); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if _, err := svc.ConnectToken(ctx, "u1", "lastfm", "tok"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("lastfm is not a token provider: err = %v", err)
	}
	if _, err := svc.ConnectToken(ctx, "u1", "listenbrainz", "  "); !errors.Is(err, ErrAuth) {
		t.Fatalf("blank token: err = %v, want ErrAuth", err)
	}
	links, _ := q.ListScrobbleLinks(ctx, "u1")
	if len(links) != 0 {
		t.Fatalf("links = %+v, want none", links)
	}
}

func TestUnlink_DropsThatProvidersPendingUploads(t *testing.T) {
	q, svc, _, _ := newProviderService(t)
	ctx := context.Background()
	var unlinked []string
	svc.OnLinkChange(func(_ context.Context, userID, provider string, active bool) {
		if !active {
			unlinked = append(unlinked, userID+"/"+provider)
		}
	})
	insertLink(t, q, "u1", "lastfm", "sk", "alice", "active")
	insertLink(t, q, "u1", "listenbrainz", "lb-token", "lbuser", "active")
	if err := svc.Enqueue(ctx, "u1", ScrobblePlay{Track: Track{Title: "T", Artist: "A"}, PlayedAt: 900_000}); err != nil {
		t.Fatal(err)
	}

	if err := svc.Unlink(ctx, "u1", "listenbrainz"); err != nil {
		t.Fatal(err)
	}
	rows := pendingRows(t, q)
	if len(rows) != 1 || rows[0].Provider != "lastfm" {
		t.Fatalf("pending after unlink = %+v, want only the lastfm row", rows)
	}
	if len(unlinked) != 1 || unlinked[0] != "u1/listenbrainz" {
		t.Fatalf("unlink hooks = %v", unlinked)
	}
}
