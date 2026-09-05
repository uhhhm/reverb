package api

import (
	"context"
	"errors"
	"testing"
)

type fakeReloader struct {
	current, next ActiveServices
	calls         int
	err           error
}

func (r *fakeReloader) Current() ActiveServices { return r.current }
func (r *fakeReloader) Reload(context.Context) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	r.current = r.next
	return nil
}

func TestReloadReadsRuntimeSnapshot(t *testing.T) {
	old, next := newFakeManager(), newFakeManager()
	cov, snc := &fakeCoverage{}, &fakeSync{}
	r := &fakeReloader{current: ActiveServices{Downloads: old}, next: ActiveServices{Downloads: next, Coverage: cov, Sync: snc}}
	s := NewServer(Deps{Reload: r})
	if s.downloads() != old {
		t.Fatal("initial runtime snapshot not used")
	}
	if err := s.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || s.downloads() != next || s.coverage() != cov || s.sync() != snc {
		t.Fatal("handlers must read the published runtime snapshot")
	}
	r.next = ActiveServices{}
	if err := s.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.downloads() != nil {
		t.Fatal("unconfigured manager must remain nil")
	}
}
func TestReloadFailureAndAbsentRuntime(t *testing.T) {
	old := newFakeManager()
	r := &fakeReloader{current: ActiveServices{Downloads: old}, err: errors.New("build failed")}
	s := NewServer(Deps{Reload: r})
	if err := s.reload(context.Background()); !errors.Is(err, r.err) {
		t.Fatal(err)
	}
	if s.downloads() != old {
		t.Fatal("failure replaced current services")
	}
	s = NewServer(Deps{Downloads: old})
	if err := s.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.downloads() != old {
		t.Fatal("static service changed")
	}
}
