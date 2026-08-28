package sync

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
)

// Signing binds a change to the device that authored it, so any peer can verify
// authorship without trusting whoever relayed it. Keys are Ed25519 and are the
// same keys as the libp2p host identity, so a device's peer ID *is* its
// verification key (see p2p.PublicKeyForPeer) and needs no separate exchange.

var (
	// ErrBadSignature means the change did not verify against the author's key.
	ErrBadSignature = errors.New("change signature does not verify")
	// ErrNoAuthorKey means we hold no public key for the claimed author.
	ErrNoAuthorKey = errors.New("no public key for change author")
)

// signingPayload builds the canonical bytes covered by a change signature.
//
// Every field that carries meaning is included and length-prefixed, so no two
// distinct changes can produce the same payload by shifting a delimiter between
// adjacent fields. valueJSON is the exact string persisted in value_json, not a
// re-marshaled copy, so signer and verifier cannot disagree over key ordering
// or whitespace.
func signingPayload(deviceID, entityType, entityID, field, valueJSON string, updatedAt, hlc, seq int64) []byte {
	parts := []string{deviceID, entityType, entityID, field, valueJSON}
	n := 0
	for _, p := range parts {
		n += 8 + len(p)
	}
	buf := make([]byte, 0, n+24+len("reverb-sync-v1"))
	// Domain separator: these signatures are valid for sync changes only.
	buf = append(buf, "reverb-sync-v1"...)
	var scratch [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(scratch[:], uint64(len(p)))
		buf = append(buf, scratch[:]...)
		buf = append(buf, p...)
	}
	for _, v := range []int64{updatedAt, hlc, seq} {
		binary.BigEndian.PutUint64(scratch[:], uint64(v))
		buf = append(buf, scratch[:]...)
	}
	return buf
}

// SignChange returns the base64 signature for a change authored by this device.
func SignChange(priv ed25519.PrivateKey, deviceID, entityType, entityID, field, valueJSON string, updatedAt, hlc, seq int64) string {
	if len(priv) == 0 {
		return ""
	}
	payload := signingPayload(deviceID, entityType, entityID, field, valueJSON, updatedAt, hlc, seq)
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))
}

// VerifyChange checks sig against the author's public key. Both are base64;
// pubKey is the raw 32-byte Ed25519 key.
func VerifyChange(pubKey, sig, deviceID, entityType, entityID, field, valueJSON string, updatedAt, hlc, seq int64) error {
	if pubKey == "" {
		return ErrNoAuthorKey
	}
	if sig == "" {
		return ErrBadSignature
	}
	raw, err := base64.StdEncoding.DecodeString(pubKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return ErrNoAuthorKey
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	payload := signingPayload(deviceID, entityType, entityID, field, valueJSON, updatedAt, hlc, seq)
	if !ed25519.Verify(ed25519.PublicKey(raw), payload, sigBytes) {
		return ErrBadSignature
	}
	return nil
}

// EncodePublicKey renders a raw Ed25519 public key for storage in
// device.public_key.
func EncodePublicKey(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}
