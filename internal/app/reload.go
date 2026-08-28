package app

import (
	"context"
	"sync/atomic"

	"fmt"
	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/core"
	"github.com/uhhhm/reverb/internal/extstream"
	"github.com/uhhhm/reverb/internal/library"
	"github.com/uhhhm/reverb/internal/resolver"
	"github.com/uhhhm/reverb/internal/wiring"
)

// matcherHolder wraps a resolver.Rematcher so it can live behind an atomic.Pointer
// (atomic.Pointer needs a concrete pointee). The wrapped matcher may be nil when no
// library is configured.
type matcherHolder struct{ m resolver.Rematcher }

// lookupHolder wraps an extstream.TrackLookup for the same reason matcherHolder
// wraps a Rematcher: atomic.Pointer needs a concrete type, and the held value
// may legitimately be nil (no search source configured).
type lookupHolder struct{ l extstream.TrackLookup }

// bundleBuilder is the seam ServiceReloader builds a fresh ServiceBundle through.
// *wiring.Builder satisfies it; tests inject a stub to drive successive matchers.
type bundleBuilder interface {
	Build(ctx context.Context) (wiring.ServiceBundle, error)
}

// ServiceReloader adapts a bundleBuilder to api.ServiceReloader. On each Reload it
// builds a fresh bundle from the current adapter_instance rows, starts the new
// download Manager (the server Stops the previous one after swapping), publishes the
// freshly-built matcher into liveMatcher so the long-lived resolver re-matches
// against the CURRENT adapter, and returns the services as the api interfaces —
// passing typed nils when a concrete service is absent so handlers see a nil
// interface, not a non-nil interface wrapping a nil pointer.
type ServiceReloader struct {
	builder bundleBuilder
	// liveMatcher is the shared holder the resolver's provider reads. It is set at
	// boot from the initial bundle and overwritten on every reload, so the resolver
	// singleton (constructed once with MatcherProvider) always reaches the live
	// matcher and never a stale captured one. Holds a *matcherHolder whose .m may be
	// nil (no library) — the provider tolerates that.
	liveMatcher atomic.Pointer[matcherHolder]
	// liveLookup is the same arrangement for the search aggregator, read by the
	// external-stream service so playing an un-owned track resolves against the
	// CURRENT search adapters rather than the ones present at boot.
	liveLookup atomic.Pointer[lookupHolder]
}

var _ api.ServiceReloader = (*ServiceReloader)(nil)

// NewServiceReloader builds a reloader over a *wiring.Builder (the production path).
func NewServiceReloader(builder *wiring.Builder) *ServiceReloader {
	return &ServiceReloader{builder: builder}
}

// NewServiceReloaderFunc builds a reloader over an arbitrary bundle-builder func.
// Used by tests to drive successive bundles (and thus matchers) without a DB.
func NewServiceReloaderFunc(build func(context.Context) (wiring.ServiceBundle, error)) *ServiceReloader {
	return &ServiceReloader{builder: bundleBuilderFunc(build)}
}

type bundleBuilderFunc func(context.Context) (wiring.ServiceBundle, error)

func (f bundleBuilderFunc) Build(ctx context.Context) (wiring.ServiceBundle, error) {
	return f(ctx)
}

// PublishMatcher installs m as the current live matcher. Called once at boot with
// the initial bundle.Matcher and again from Reload after each rebuild. m may be nil.
func (r *ServiceReloader) PublishMatcher(m resolver.Rematcher) {
	r.liveMatcher.Store(&matcherHolder{m: m})
}

// MatcherProvider returns the resolver.Service provider: a func that reads the
// current live matcher on every call, so the resolver follows hot-reloads instead
// of capturing a stale matcher. Returns nil safely before any publish or when no
// library is configured.
func (r *ServiceReloader) MatcherProvider() func() resolver.Rematcher {
	return func() resolver.Rematcher {
		h := r.liveMatcher.Load()
		if h == nil {
			return nil
		}
		return h.m
	}
}

// PublishTrackLookup installs l as the current live search lookup. Called once at
// boot and again after every rebuild. l may be nil.
func (r *ServiceReloader) PublishTrackLookup(l extstream.TrackLookup) {
	r.liveLookup.Store(&lookupHolder{l: l})
}

// TrackLookupProvider returns a func reading the current live lookup, so the
// long-lived external-stream service follows hot-reloads instead of capturing a
// stale aggregator. Returns nil safely before any publish.
func (r *ServiceReloader) TrackLookupProvider() func() extstream.TrackLookup {
	return func() extstream.TrackLookup {
		h := r.liveLookup.Load()
		if h == nil {
			return nil
		}
		return h.l
	}
}

func (r *ServiceReloader) Reload(ctx context.Context) (library.LibraryAdapter, api.Streamer, api.CoverageService, api.DownloadManager, api.SyncService, error) {
	bundle, err := r.builder.Build(ctx)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// Publish the freshly-built matcher (may be nil) so the resolver singleton
	// re-matches against the live adapter rather than the one it was wired with.
	r.PublishMatcher(bundle.Matcher)
	// Same for the search aggregator (may be nil when no search source is on).
	if bundle.Aggregator != nil {
		r.PublishTrackLookup(bundle.Aggregator)
	} else {
		r.PublishTrackLookup(nil)
	}

	// LibraryAdapter is itself an interface; a nil bundle.Library is a usable nil
	// interface and the libraryReady guard handles it.
	lib := bundle.Library

	var srch api.Streamer
	if bundle.Aggregator != nil {
		srch = bundle.Aggregator
	}

	// Guard against the non-nil-interface-wrapping-nil-pointer trap: only set the
	// interface when the concrete service is present.
	var cov api.CoverageService
	if bundle.Coverage != nil {
		cov = bundle.Coverage
	}

	var dl api.DownloadManager
	if bundle.Manager != nil {
		bundle.Manager.Start()
		dl = bundle.Manager
	}

	var snc api.SyncService
	if bundle.Sync != nil {
		snc = bundle.Sync
	}

	return lib, srch, cov, dl, snc, nil
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
