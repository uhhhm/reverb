package p2p

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/uhhhm/reverb/internal/store/db"
)

func TestSearchCredentialsOnlyReachTrustedPeer(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)
	store := newTrustStore(t)
	called := false
	RegisterSearchCredentialsHandler(server, NewGuard(store), func(context.Context) (SearchCredentials, error) {
		called = true
		return SearchCredentials{ClientID: "desktop-id", ClientSecret: "desktop-secret"}, nil
	})
	if _, err := RequestSearchCredentials(ctx, client, server.ID()); err == nil {
		t.Fatal("unpaired peer received search credentials")
	}
	if called {
		t.Fatal("unpaired peer reached credential provider")
	}
	if err := store.CreateDevice(ctx, db.CreateDeviceParams{ID: "phone-device", Name: "Phone", TokenHash: "phone-token"}); err != nil {
		t.Fatal(err)
	}
	if err := NewGuard(store).Trust(ctx, client.ID(), "phone-device", "Phone"); err != nil {
		t.Fatal(err)
	}
	got, err := RequestSearchCredentials(ctx, client, server.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != "desktop-id" || got.ClientSecret != "desktop-secret" {
		t.Fatal("paired peer did not receive credentials")
	}
}

func TestSearchCredentialsReportAbsenceDistinctly(t *testing.T) {
	ctx := context.Background()
	server, client := newLinkedHosts(t)
	store := newTrustStore(t)
	if err := store.CreateDevice(ctx, db.CreateDeviceParams{ID: "phone-device", Name: "Phone", TokenHash: "phone-token"}); err != nil {
		t.Fatal(err)
	}
	guard := NewGuard(store)
	if err := guard.Trust(ctx, client.ID(), "phone-device", "Phone"); err != nil {
		t.Fatal(err)
	}
	provideErr := ErrNoSearchCredentials
	RegisterSearchCredentialsHandler(server, guard, func(context.Context) (SearchCredentials, error) {
		return SearchCredentials{}, provideErr
	})
	if _, err := RequestSearchCredentials(ctx, client, server.ID()); !errors.Is(err, ErrNoSearchCredentials) {
		t.Fatalf("not configured: err = %v, want ErrNoSearchCredentials", err)
	}
	// A failure reading the configuration must not tell the phone to forget.
	provideErr = errors.New("database is locked")
	if _, err := RequestSearchCredentials(ctx, client, server.ID()); err == nil || errors.Is(err, ErrNoSearchCredentials) {
		t.Fatalf("provider failure: err = %v, want a non-absence error", err)
	}
}

func TestOnlyEveryDeviceSayingNoMakesCredentialsStale(t *testing.T) {
	unreachable := fmt.Errorf("device b: %w", errNoReachablePeer)
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{ErrNoSearchCredentials, true},
		{errors.Join(fmt.Errorf("device a: %w", ErrNoSearchCredentials), fmt.Errorf("device b: %w", ErrNoSearchCredentials)), true},
		{errors.Join(fmt.Errorf("device a: %w", ErrNoSearchCredentials), unreachable), false},
		{errors.Join(unreachable), false},
	} {
		if got := onlyNoSearchCredentials(tc.err); got != tc.want {
			t.Errorf("onlyNoSearchCredentials(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestCopySearchCredentialsWithNoPairedDeviceIsStale(t *testing.T) {
	store := newTrustStore(t)
	client, _ := newLinkedHosts(t)
	d := NewDelegator(func() host.Host { return client }, func() *Guard { return NewGuard(store) }, store)
	if _, err := d.CopySearchCredentials(context.Background()); !errors.Is(err, ErrNoSearchCredentials) {
		t.Fatalf("err = %v, want ErrNoSearchCredentials", err)
	}
}
