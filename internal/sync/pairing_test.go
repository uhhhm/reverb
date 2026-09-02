package sync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uhhhm/reverb/internal/store"
	"github.com/uhhhm/reverb/internal/store/db"
	syncpkg "github.com/uhhhm/reverb/internal/sync"
)

func newTestStorePairing(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/pairing.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestPairingGenerateFormat(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	code, expiresAt, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if len(code) != 9 {
		t.Fatalf("code len = %d, want 9 (XXXX-XXXX)", len(code))
	}
	if code[4] != '-' {
		t.Fatalf("code %q missing dash at pos 4", code)
	}
	stripped := strings.ReplaceAll(code, "-", "")
	if len(stripped) != 8 {
		t.Fatalf("stripped len = %d, want 8", len(stripped))
	}
	for _, c := range stripped {
		if !strings.ContainsRune("ABCDEFGHJKLMNPQRSTUVWXYZ23456789", c) {
			t.Fatalf("code char %q not in allowed alphabet", c)
		}
		if c == 'I' || c == 'O' || c == '0' || c == '1' {
			t.Fatalf("code contains ambiguous char %q", c)
		}
	}
	now := time.Now().Unix()
	if expiresAt < now+590 || expiresAt > now+610 {
		t.Fatalf("expiresAt %d not ~10m from now %d", expiresAt, now)
	}
	// stored stripped
	pc, err := st.Q().GetPairingCode(ctx, stripped)
	if err != nil {
		t.Fatalf("GetPairingCode stripped: %v", err)
	}
	if pc.Code != stripped {
		t.Fatalf("stored code %q != stripped %q", pc.Code, stripped)
	}
	if pc.ExpiresAt != expiresAt {
		t.Fatalf("stored expiresAt %d != returned %d", pc.ExpiresAt, expiresAt)
	}
}

func TestPairingRedeemSuccess(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deviceID, token, err := svc.Redeem(ctx, code, "my laptop")
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if !strings.HasPrefix(deviceID, "dev_") {
		t.Fatalf("deviceID %q missing dev_ prefix", deviceID)
	}
	if len(token) != 43 {
		t.Fatalf("token len %d, want 43", len(token))
	}
	h := sha256.Sum256([]byte(token))
	hashHex := hex.EncodeToString(h[:])
	dev, err := st.Q().GetDeviceByTokenHash(ctx, hashHex)
	if err != nil {
		t.Fatalf("GetDeviceByTokenHash: %v", err)
	}
	if dev.ID != deviceID {
		t.Fatalf("device ID mismatch %q vs %q", dev.ID, deviceID)
	}
	if dev.Name != "my laptop" {
		t.Fatalf("device Name %q want %q", dev.Name, "my laptop")
	}
	stripped := strings.ReplaceAll(code, "-", "")
	pc, err := st.Q().GetPairingCode(ctx, stripped)
	if err != nil {
		t.Fatal(err)
	}
	if !pc.UsedAt.Valid {
		t.Fatal("pairing code not marked used")
	}
	if !pc.UsedByDeviceID.Valid || pc.UsedByDeviceID.String != deviceID {
		t.Fatalf("used_by_device_id %v want %q", pc.UsedByDeviceID, deviceID)
	}
}

func TestPairingRedeemWithDashesAndCase(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Try redeem with lower case and spaces
	raw := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	// raw like "abcd efgh"
	if raw == code {
		t.Fatal("raw variant same as code, need different case")
	}
	deviceID, token, err := svc.Redeem(ctx, raw, "laptop2")
	if err != nil {
		t.Fatalf("Redeem with dashes/case variant %q: %v", raw, err)
	}
	if deviceID == "" || token == "" {
		t.Fatal("empty deviceID/token")
	}
	// also ensure stripping works with mixed dashes/spaces
	code2, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mixed := "  " + strings.ToLower(code2[:4]) + " - " + strings.ToLower(code2[5:]) + "  "
	_, _, err = svc.Redeem(ctx, mixed, "laptop3")
	if err != nil {
		t.Fatalf("Redeem mixed variant %q: %v", mixed, err)
	}
}

func TestPairingRedeemExpired(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	expired := "ABCD2345"
	if err := st.Q().CreatePairingCode(ctx, db.CreatePairingCodeParams{Code: expired, ExpiresAt: time.Now().Unix() - 10}); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.Redeem(ctx, expired, "laptop")
	if err != syncpkg.ErrCodeExpired {
		t.Fatalf("want ErrCodeExpired, got %v", err)
	}
	// with dash formatting also expired
	_, _, err = svc.Redeem(ctx, "ABCD-2345", "laptop")
	if err != syncpkg.ErrCodeExpired {
		t.Fatalf("dash variant: want ErrCodeExpired, got %v", err)
	}
}

func TestPairingRedeemUsed(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Redeem(ctx, code, "first"); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	_, _, err = svc.Redeem(ctx, code, "second")
	if err != syncpkg.ErrCodeUsed {
		t.Fatalf("want ErrCodeUsed, got %v", err)
	}
}

func TestPairingRedeemInvalid(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	_, _, err := svc.Redeem(ctx, "ZZZZ-ZZZZ", "laptop")
	if err != syncpkg.ErrCodeInvalid {
		t.Fatalf("want ErrCodeInvalid for unknown code, got %v", err)
	}
	_, _, err = svc.Redeem(ctx, "bad!", "laptop")
	if err != syncpkg.ErrCodeInvalid {
		t.Fatalf("want ErrCodeInvalid for malformed, got %v", err)
	}
	_, _, err = svc.Redeem(ctx, "", "laptop")
	if err != syncpkg.ErrCodeInvalid {
		t.Fatalf("want ErrCodeInvalid for empty, got %v", err)
	}
}

func TestPairingAuthenticateByTokenValid(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := svc.Redeem(ctx, code, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := svc.AuthenticateByToken(ctx, token)
	if err != nil {
		t.Fatalf("AuthenticateByToken: %v", err)
	}
	if dev == nil {
		t.Fatal("nil device")
	}
	if dev.Name != "laptop" {
		t.Fatalf("device name %q want laptop", dev.Name)
	}
	// last_seen should have been touched (at least not zero)
	if dev.LastSeen == 0 {
		t.Fatal("last_seen not set")
	}
}

func TestPairingAuthenticateByTokenInvalid(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	_, err := svc.AuthenticateByToken(ctx, "invalidtokeninvalidtokeninvalidtoken123")
	if err != syncpkg.ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
	// also random token never issued
	_, err = svc.AuthenticateByToken(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != syncpkg.ErrInvalidToken {
		t.Fatalf("want ErrInvalidToken for random, got %v", err)
	}
}

func TestPairingEnsureServerDevice(t *testing.T) {
	st := newTestStorePairing(t)
	ctx := context.Background()

	id1, err := syncpkg.EnsureServerDevice(ctx, st.Q())
	if err != nil {
		t.Fatalf("EnsureServerDevice: %v", err)
	}
	if !strings.HasPrefix(id1, "dev_") {
		t.Fatalf("deviceID %q missing dev_ prefix", id1)
	}
	dev, err := st.Q().GetDeviceByID(ctx, id1)
	if err != nil {
		t.Fatal(err)
	}
	if dev.IsServer != 1 {
		t.Fatalf("is_server %d want 1", dev.IsServer)
	}
	if dev.Name != "server" {
		t.Fatalf("name %q want server", dev.Name)
	}
	val, err := st.Q().GetSetting(ctx, "server_device_id")
	if err != nil {
		t.Fatalf("GetSetting server_device_id: %v", err)
	}
	if val != id1 {
		t.Fatalf("server_device_id setting %q != %q", val, id1)
	}
	// idempotency: second call returns same ID, no duplicate row
	id2, err := syncpkg.EnsureServerDevice(ctx, st.Q())
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id1 {
		t.Fatalf("second EnsureServerDevice %q != first %q", id2, id1)
	}
	devices, err := st.Q().ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	serverCount := 0
	for _, d := range devices {
		if d.IsServer == 1 {
			serverCount++
		}
	}
	if serverCount != 1 {
		t.Fatalf("server device count %d want 1", serverCount)
	}
}

// A peer that already has a sync identity pairs into it, so the token and the
// device the responder binds name the device that actually authors changes.
func TestRedeemAsBindsSuppliedDeviceID(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := "dev_" + uuid.NewString()
	got, token, err := svc.RedeemAs(ctx, code, "laptop", want)
	if err != nil {
		t.Fatalf("RedeemAs: %v", err)
	}
	if got != want {
		t.Fatalf("redeemed as %q, want %q", got, want)
	}
	dev, err := st.Q().GetDeviceByID(ctx, want)
	if err != nil {
		t.Fatalf("device row missing: %v", err)
	}
	sum := sha256.Sum256([]byte(token))
	if dev.TokenHash != hex.EncodeToString(sum[:]) {
		t.Fatal("device row does not carry the token that was handed out")
	}
}

// The row may already exist: a peer's device is announced before it pairs.
// Re-claiming it reissues the token rather than failing on the primary key.
func TestRedeemAsReclaimsExistingDeviceRow(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	id := "dev_" + uuid.NewString()
	if err := st.Q().CreateDevice(ctx, db.CreateDeviceParams{ID: id, Name: "announced", TokenHash: ""}); err != nil {
		t.Fatal(err)
	}
	code, _, err := svc.GenerateCode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, token, err := svc.RedeemAs(ctx, code, "laptop", id)
	if err != nil {
		t.Fatalf("RedeemAs onto an announced device: %v", err)
	}
	if got != id || token == "" {
		t.Fatalf("got (%q, %q), want (%q, non-empty)", got, token, id)
	}
	dev, err := st.Q().GetDeviceByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if dev.TokenHash == "" {
		t.Fatal("re-claim did not issue a token")
	}
}

// The ID becomes a row key and travels in the author field of every change, so
// a peer cannot offer a free-form string.
func TestRedeemAsRejectsMalformedDeviceID(t *testing.T) {
	st := newTestStorePairing(t)
	svc := syncpkg.NewPairingService(st.Q())
	ctx := context.Background()

	for _, id := range []string{"nope", "dev_", "dev_not-a-uuid", "'; DROP TABLE device;--"} {
		code, _, err := svc.GenerateCode(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.RedeemAs(ctx, code, "laptop", id); !errors.Is(err, syncpkg.ErrDeviceIDInvalid) {
			t.Errorf("RedeemAs(%q) error = %v, want ErrDeviceIDInvalid", id, err)
		}
	}
}
