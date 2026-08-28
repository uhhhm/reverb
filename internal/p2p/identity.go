package p2p

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/uhhhm/reverb/internal/store/db"
)

// hostKeySetting is the settings key holding this node's libp2p private key.
const hostKeySetting = "p2p_host_key"

// IdentityStore is the minimal settings seam for persisting the host key.
// *db.Queries satisfies it.
type IdentityStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error
}

// LoadOrCreateIdentity returns this node's stable libp2p private key, creating
// and persisting one on first use.
//
// The key is the node's identity in two senses: it is the libp2p peer ID that
// pairing binds trust to, and — because Ed25519 peer IDs carry their public key
// inline — it is the key other devices verify this device's changes with. It
// must therefore survive restarts: a fresh key on every boot would invalidate
// every pairing and orphan every signature this device ever produced.
func LoadOrCreateIdentity(ctx context.Context, store IdentityStore) (crypto.PrivKey, error) {
	if store == nil {
		return nil, fmt.Errorf("no identity store")
	}
	raw, err := store.GetSetting(ctx, hostKeySetting)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("load p2p host key: %w", err)
		}
	} else if raw != "" {
		b, err := base64.StdEncoding.DecodeString(raw)
		if err == nil {
			if priv, err := crypto.UnmarshalPrivateKey(b); err == nil {
				return priv, nil
			}
		}
		// A corrupt key is not recoverable by regenerating: that would change
		// our peer ID and silently break every existing pairing. Fail loudly.
		return nil, fmt.Errorf("stored p2p host key is unreadable; re-pairing required to reset")
	}
	priv, _, err := crypto.GenerateKeyPair(crypto.Ed25519, -1)
	if err != nil {
		return nil, err
	}
	b, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	if err := store.UpsertSetting(ctx, db.UpsertSettingParams{
		Key:   hostKeySetting,
		Value: base64.StdEncoding.EncodeToString(b),
	}); err != nil {
		return nil, err
	}
	return priv, nil
}

// PublicKeyForPeer extracts the verification key embedded in an Ed25519 peer ID.
// libp2p encodes small keys directly in the peer ID, so no key distribution is
// needed: knowing a peer ID is knowing its public key.
func PublicKeyForPeer(pid peer.ID) (crypto.PubKey, error) {
	return pid.ExtractPublicKey()
}
