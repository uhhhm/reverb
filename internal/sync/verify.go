package sync

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uhhhm/reverb/internal/store/db"
)

// DeviceKeyStore is the seam for reading and recording device verification keys.
type DeviceKeyStore interface {
	GetDeviceByID(ctx context.Context, id string) (db.Device, error)
	SetDevicePublicKey(ctx context.Context, arg db.SetDevicePublicKeyParams) error
}

// ErrKeyConflict means a device already has a different public key on record.
var ErrKeyConflict = errors.New("device public key does not match the one on record")

// PublicKeyFor returns the stored verification key for deviceID, or "" if the
// device is unknown or has no key yet.
func (s *SyncStore) PublicKeyFor(ctx context.Context, deviceID string) string {
	ks, ok := any(s.q).(DeviceKeyStore)
	if !ok {
		return ""
	}
	dev, err := ks.GetDeviceByID(ctx, deviceID)
	if err != nil {
		return ""
	}
	return dev.PublicKey
}

// RecordDeviceKey binds a public key to a device on first sight and refuses to
// change it afterwards.
//
// Trust-on-first-use is what makes transitive sync possible: to accept a change
// authored by a device we never paired with, we must learn its key from a peer.
// A peer that introduces an unknown device is only asserting data it could have
// invented under its own name anyway, so this grants it nothing new. Rebinding
// an existing device's key is the dangerous case — that would let one peer take
// over another's identity — so it is rejected outright.
func (s *SyncStore) RecordDeviceKey(ctx context.Context, deviceID, publicKey string) error {
	if deviceID == "" || publicKey == "" {
		return nil
	}
	ks, ok := any(s.q).(DeviceKeyStore)
	if !ok {
		return nil
	}
	dev, err := ks.GetDeviceByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Unknown device: nothing to bind to yet. Callers that want to
			// introduce a device create the row first.
			return sql.ErrNoRows
		}
		return err
	}
	if dev.PublicKey != "" {
		if dev.PublicKey != publicKey {
			return ErrKeyConflict
		}
		return nil
	}
	return ks.SetDevicePublicKey(ctx, db.SetDevicePublicKeyParams{PublicKey: publicKey, ID: deviceID})
}

// VerifyChangeAuthorship checks that ch really was authored by the device it
// names. It is the gate that lets a peer relay a third party's changes: the
// signature travels with the change, so the relaying peer never has to be
// trusted to speak for the author.
func (s *SyncStore) VerifyChangeAuthorship(ctx context.Context, ch SyncChange) error {
	if ch.DeviceID == "" {
		return ErrNoAuthorKey
	}
	pub := s.PublicKeyFor(ctx, ch.DeviceID)
	if pub == "" {
		return ErrNoAuthorKey
	}
	valueJSON := ch.ValueJSON
	if valueJSON == "" {
		var err error
		valueJSON, err = marshalValue(ch)
		if err != nil {
			return ErrBadSignature
		}
	}
	return VerifyChange(pub, ch.Sig, ch.DeviceID, ch.EntityType, ch.EntityID, ch.Field, valueJSON, ch.UpdatedAt, ch.HLC, ch.Seq)
}
