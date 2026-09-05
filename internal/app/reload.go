package app

import (
	"context"
	"sync"
	"sync/atomic"

	"fmt"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/extstream"
	"github.com/uhhhm/reverb/internal/resolver"
	"github.com/uhhhm/reverb/internal/wiring"
)

// bundleBuilder constructs candidates without publishing them.
type bundleBuilder interface {
	Build(context.Context) (wiring.ServiceBundle, error)
}
type managedDownloads interface {
	Start()
	Stop()
	RedispatchQueued()
}
type runtimeSnapshot struct {
	services api.ActiveServices
	matcher  resolver.Rematcher
	lookup   extstream.TrackLookup
	manager  managedDownloads
}

// ServiceReloader is the single owner of reloadable services and download workers.
// Readers retain a snapshot; replacement serializes with shutdown. Requests that
// already captured an old service may finish while its workers retire.
type ServiceReloader struct {
	builder bundleBuilder
	mu      sync.Mutex
	closed  bool
	live    atomic.Pointer[runtimeSnapshot]
}

var _ api.ServiceReloader = (*ServiceReloader)(nil)

func NewServiceReloader(builder *wiring.Builder) *ServiceReloader {
	return &ServiceReloader{builder: builder}
}
func NewServiceReloaderFunc(build func(context.Context) (wiring.ServiceBundle, error)) *ServiceReloader {
	return &ServiceReloader{builder: bundleBuilderFunc(build)}
}

type bundleBuilderFunc func(context.Context) (wiring.ServiceBundle, error)

func (f bundleBuilderFunc) Build(ctx context.Context) (wiring.ServiceBundle, error) { return f(ctx) }

func snapshot(bundle wiring.ServiceBundle) *runtimeSnapshot {
	n := &runtimeSnapshot{matcher: bundle.Matcher}
	n.services.Library = bundle.Library
	if bundle.Aggregator != nil {
		n.services.Search = bundle.Aggregator
		n.lookup = bundle.Aggregator
	}
	if bundle.Coverage != nil {
		n.services.Coverage = bundle.Coverage
	}
	if bundle.Manager != nil {
		n.services.Downloads = bundle.Manager
		n.manager = bundle.Manager
	}
	if bundle.Sync != nil {
		n.services.Sync = bundle.Sync
	}
	return n
}

// Initialize installs the boot bundle without starting workers. Called before
// requests or background work; Build remains safe to use in construction tests.
func (r *ServiceReloader) Initialize(bundle wiring.ServiceBundle) { r.live.Store(snapshot(bundle)) }
func (r *ServiceReloader) Current() api.ActiveServices {
	if n := r.live.Load(); n != nil {
		return n.services
	}
	return api.ActiveServices{}
}
func (r *ServiceReloader) MatcherProvider() func() resolver.Rematcher {
	return func() resolver.Rematcher {
		if n := r.live.Load(); n != nil {
			return n.matcher
		}
		return nil
	}
}
func (r *ServiceReloader) TrackLookupProvider() func() extstream.TrackLookup {
	return func() extstream.TrackLookup {
		if n := r.live.Load(); n != nil {
			return n.lookup
		}
		return nil
	}
}
func (r *ServiceReloader) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return fmt.Errorf("application runtime is closed")
	}
	bundle, err := r.builder.Build(ctx)
	if err != nil {
		return err
	}
	r.replace(snapshot(bundle))
	return nil
}

// replace preserves the download handoff order: recover/start the candidate,
// publish all request services and lookups, stop the previous manager, then
// redispatch jobs it requeued. The caller holds mu.
func (r *ServiceReloader) replace(next *runtimeSnapshot) {
	old := r.live.Load()
	if next.manager != nil {
		next.manager.Start()
	}
	r.live.Store(next)
	if old != nil && old.manager != nil && old.manager != next.manager {
		old.manager.Stop()
		if next.manager != nil {
			next.manager.RedispatchQueued()
		}
	}
}
func (r *ServiceReloader) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		if n := r.live.Load(); n != nil && n.manager != nil {
			n.manager.Start()
		}
	}
}
func (r *ServiceReloader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if n := r.live.Load(); n != nil && n.manager != nil {
		n.manager.Stop()
	}
}

// ProviderLookup adapts a live-lookup provider to extstream.TrackLookup, so the
// long-lived stream service never captures a specific aggregator.
type ProviderLookup struct{ Get func() extstream.TrackLookup }

func (p ProviderLookup) GetTrack(ctx context.Context, source, externalID string) (core.ExternalResult, error) {
	l := p.Get()
	if l == nil {
		return core.ExternalResult{}, fmt.Errorf("no search source configured")
	}
	return l.GetTrack(ctx, source, externalID)
}
