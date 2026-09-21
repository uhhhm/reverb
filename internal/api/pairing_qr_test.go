package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uhhhm/reverb/internal/p2p"
	"github.com/uhhhm/reverb/internal/store"
)

func withP2PHost(t *testing.T, srv *Server, st *store.Store) *p2p.Host {
	t.Helper()
	h, err := p2p.NewHostWith(context.Background(), nil, 0, p2p.HostOptions{NoDiscovery: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	guard := p2p.NewGuard(st.Q())
	srv.deps.P2P = func() *p2p.Host { return h }
	srv.deps.P2PGuard = func() *p2p.Guard { return guard }
	return h
}

// A new code comes with the same code as a scannable pairing link carrying
// where this device can be dialled, and that link drawn as a QR code.
func TestPairingCodeCarriesAQRPayload(t *testing.T) {
	srv, st := newPairingTestServer(t)
	h := withP2PHost(t, srv, st)
	if len(h.DialAddrs()) == 0 {
		t.Skip("no non-loopback interface to advertise")
	}
	rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /pairing/code = %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Code      string `json:"code"`
		ExpiresAt int64  `json:"expiresAt"`
		QRPayload string `json:"qrPayload"`
		QRSvg     string `json:"qrSvg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	payload, target, err := p2p.ParsePairPayload(got.QRPayload, time.Now())
	if err != nil {
		t.Fatalf("payload %q: %v", got.QRPayload, err)
	}
	if payload.Code != strings.ReplaceAll(got.Code, "-", "") || payload.ExpiresAt != got.ExpiresAt {
		t.Fatalf("payload %+v does not carry code %s/%d", payload, got.Code, got.ExpiresAt)
	}
	if target.ID.String() != h.ID() {
		t.Fatalf("payload names peer %s, want %s", target.ID, h.ID())
	}
	if !strings.HasPrefix(got.QRSvg, "<svg") || !strings.Contains(got.QRSvg, "<path") {
		t.Fatalf("qrSvg = %.80q", got.QRSvg)
	}
}

// Without a p2p host there is nowhere to dial, so there is no QR code; the
// typed code still works.
func TestPairingCodeWithoutP2POmitsTheQRCode(t *testing.T) {
	srv, _ := newPairingTestServer(t)
	rec := doPostJSON(t, srv, "/api/v1/pairing/code", `{}`, nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "qrPayload") {
		t.Fatalf("POST /pairing/code = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRedeemQRRefusesUnusablePayloads(t *testing.T) {
	srv, st := newPairingTestServer(t)
	withP2PHost(t, srv, st)
	link := func(version, exp string) string {
		v := url.Values{}
		v.Set("v", version)
		v.Set("code", "AB12CD34")
		v.Set("exp", exp)
		v.Set("peer", "12D3KooWD3eckifWpRn9wQpMG9R9hX3sD158z7EqHWmweQAJU5SA")
		v.Set("addr", "/ip4/192.0.2.1/tcp/4331")
		return "reverb://pair?" + v.Encode()
	}
	future := time.Now().Add(time.Minute).Unix()
	past := time.Now().Add(-time.Minute).Unix()
	cases := map[string]struct {
		body     string
		status   int
		errMatch string
	}{
		"malformed":       {`{"payload":"AB12-CD34","deviceName":"phone"}`, http.StatusBadRequest, "not a Reverb pairing code"},
		"unknown version": {mustJSON(t, map[string]string{"payload": link("9", strconv.FormatInt(future, 10)), "deviceName": "phone"}), http.StatusBadRequest, "newer version"},
		"expired":         {mustJSON(t, map[string]string{"payload": link("1", strconv.FormatInt(past, 10)), "deviceName": "phone"}), http.StatusGone, "expired"},
		"no device name":  {mustJSON(t, map[string]string{"payload": link("1", strconv.FormatInt(future, 10))}), http.StatusBadRequest, "required"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := doPostJSON(t, srv, "/api/v1/p2p/pair/redeem-qr", c.body, nil)
			if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.errMatch) {
				t.Fatalf("status %d body %s, want %d containing %q", rec.Code, rec.Body.String(), c.status, c.errMatch)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
