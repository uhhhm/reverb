package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/catalog"
	"github.com/uhhhm/reverb/internal/play"
	"github.com/uhhhm/reverb/internal/store"
)

// The plays table has a text primary key and no AUTOINCREMENT, so SQLite hands
// a new row the highest free rowid: delete the newest play and the next one
// recorded takes its place. The taste profile folds plays by rowid, so it has
// to be able to tell the replacement from the play it already folded in.
func TestTastePlayIDAtIdentifiesAPlayThatTookARemovedRowid(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/taste.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	n := 0
	ids := func() string { n++; return string(rune('a' + n)) }
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	svc := play.NewService(st.Q(), catalog.NewService(st.Q(), now, ids), now, ids)

	qualified := true
	record := func(title, artist string) {
		t.Helper()
		in := play.PlayInput{Title: title, Artist: artist, PlayedAt: 1_700_000_000, Completed: true, Qualified: &qualified}
		if err := svc.Record(ctx, "u1", in); err != nil {
			t.Fatal(err)
		}
	}
	record("One", "A")
	record("Two", "A")

	before, err := st.Q().ListTastePlaysAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("stored %d taste plays, want 2", len(before))
	}
	newest := before[len(before)-1]

	if err := svc.Delete(ctx, "u1", newest.ID); err != nil {
		t.Fatal(err)
	}
	record("Three", "B")

	after, err := st.Q().ListTastePlaysAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("stored %d taste plays after the swap, want 2", len(after))
	}
	replacement := after[len(after)-1]
	if replacement.Seq != newest.Seq {
		t.Fatalf("expected the new play to take the removed play's rowid %d, got %d", newest.Seq, replacement.Seq)
	}
	if replacement.ID == newest.ID {
		t.Fatal("the replacement play must have its own id")
	}

	// A fold sitting at the removed play's rowid sees the replacement's id, not
	// the one it folded in, which is what tells it a play was removed.
	got, err := st.Q().TastePlayIDAt(ctx, newest.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if got != replacement.ID {
		t.Fatalf("TastePlayIDAt(%d) = %q, want the replacement %q", newest.Seq, got, replacement.ID)
	}

	// A rowid no play holds reports empty rather than an error.
	missing, err := st.Q().TastePlayIDAt(ctx, replacement.Seq+100)
	if err != nil {
		t.Fatal(err)
	}
	if missing != "" {
		t.Fatalf("TastePlayIDAt of an unused rowid = %q, want \"\"", missing)
	}
}
