package p2p

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/store/db"
	reverbsync "github.com/uhhhm/reverb/internal/sync"
)

// DeviceKeyStore is the store seam for recording peer devices and their keys.
// *db.Queries satisfies it.
type DeviceKeyStore interface {
	UpsertPeerDevice(ctx context.Context, arg db.UpsertPeerDeviceParams) error
	ListDevicesWithKeys(ctx context.Context) ([]db.ListDevicesWithKeysRow, error)
	GetDeviceByID(ctx context.Context, id string) (db.Device, error)
}

// PublicKeyBase64 returns the raw Ed25519 verification key embedded in an
// Ed25519 peer ID, base64 encoded for storage in device.public_key.
func PublicKeyBase64(pid peer.ID) (string, error) {
	pub, err := pid.ExtractPublicKey()
	if err != nil {
		return "", err
	}
	raw, err := pub.Raw()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// randomTokenHash produces a placeholder token_hash for a peer's device row.
// The column is NOT NULL UNIQUE and is only meaningful for devices that
// authenticate to *us* over HTTP; a peer device never does, so the value must
// simply be unique and unguessable rather than derived from anything.
func randomTokenHash() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "peer:" + base64.RawURLEncoding.EncodeToString(b), nil
}

// RecordPeerDevice creates or updates the local row for a remote device and
// binds its verification key. The underlying upsert never overwrites a key that
// is already set, so a peer cannot rebind another device's identity.
func RecordPeerDevice(ctx context.Context, store DeviceKeyStore, deviceID, name, publicKey string) error {
	if store == nil || deviceID == "" || publicKey == "" {
		return nil
	}
	th, err := randomTokenHash()
	if err != nil {
		return err
	}
	return store.UpsertPeerDevice(ctx, db.UpsertPeerDeviceParams{
		ID:        deviceID,
		Name:      name,
		TokenHash: th,
		PublicKey: publicKey,
	})
}

// LocalDeviceAnnouncements returns the device keys this node knows, to be
// gossiped to peers so they can verify changes from devices they have not
// paired with directly.
func LocalDeviceAnnouncements(ctx context.Context, store DeviceKeyStore) []reverbsync.DeviceAnnounce {
	if store == nil {
		return nil
	}
	rows, err := store.ListDevicesWithKeys(ctx)
	if err != nil {
		return nil
	}
	out := make([]reverbsync.DeviceAnnounce, 0, len(rows))
	for _, r := range rows {
		out = append(out, reverbsync.DeviceAnnounce{
			DeviceID:  r.ID,
			PublicKey: r.PublicKey,
			Name:      r.Name,
		})
	}
	return out
}

// ApplyDeviceAnnouncements records keys for devices we do not know yet.
//
// This is trust-on-first-use: the first key seen for a device wins and is never
// replaced. A peer introducing an unknown device asserts nothing it could not
// have claimed under its own identity, but silently rebinding a device we
// already know would be identity takeover, so conflicts are logged and dropped.
func ApplyDeviceAnnouncements(ctx context.Context, store DeviceKeyStore, announces []reverbsync.DeviceAnnounce) {
	if store == nil {
		return
	}
	for _, a := range announces {
		if a.DeviceID == "" || a.PublicKey == "" {
			continue
		}
		if _, err := base64.StdEncoding.DecodeString(a.PublicKey); err != nil {
			continue
		}
		existing, err := store.GetDeviceByID(ctx, a.DeviceID)
		if err == nil && existing.PublicKey != "" {
			if existing.PublicKey != a.PublicKey {
				log.Printf("p2p: refusing to rebind public key for device %s", a.DeviceID)
			}
			continue
		}
		if err := RecordPeerDevice(ctx, store, a.DeviceID, a.Name, a.PublicKey); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("p2p: record device %s: %v", a.DeviceID, err)
		}
	}
}
