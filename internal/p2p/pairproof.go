package p2p

import (
	"bytes"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
)

// Pairing proves possession of the code without sending it. The code is only
// 2^40 -- small enough that a value derived from it could be brute-forced
// offline in minutes if the derivation were cheap -- so both sides stretch it
// with a KDF and prove knowledge with an HMAC over the exchange transcript.
// The attempt limiter bounds online guessing; the stretching bounds offline
// guessing of a captured proof.
const (
	pairNonceBytes = 32
	// pairKDFIterations is sized so recovering a 40-bit code from a captured
	// proof costs far more than the code's 10 minute life: 2^40 guesses at
	// this many compressions each is months of GPU work, by which time the
	// code is spent or expired.
	pairKDFIterations = 200_000
	pairKDFKeyBytes   = 32
)

// Proof labels keep the two directions of the mutual proof distinct, so a
// redeemer proof reflected back by a rogue responder cannot verify.
const (
	pairLabelRedeemer  = "redeemer"
	pairLabelResponder = "responder"
)

var errPairNonce = errors.New("pairing nonce must be 32 random bytes")

// newPairNonce returns a fresh nonce for one pairing exchange.
func newPairNonce() (string, error) {
	b := make([]byte, pairNonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b), nil
}

// validPairNonce checks that a peer's nonce has the shape this protocol sends.
// A peer that cannot open with one speaks a protocol that proves nothing, so
// the exchange is refused rather than downgraded to trusting it.
func validPairNonce(s string) error {
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil || len(b) != pairNonceBytes {
		return errPairNonce
	}
	return nil
}

// pairTranscript renders proof fields unambiguously: each field is
// length-prefixed, so a device name or ID containing the separator cannot
// shift a boundary.
func pairTranscript(fields ...string) []byte {
	var b bytes.Buffer
	for _, f := range fields {
		b.WriteString(strconv.Itoa(len(f)))
		b.WriteByte(':')
		b.WriteString(f)
	}
	return b.Bytes()
}

// pairContext is everything a possession proof is bound to. The nonces make
// each exchange's proofs unique; the identities stop a proof captured from one
// exchange being replayed in another.
type pairContext struct {
	clientNonce   string
	serverNonce   string
	deviceID      string
	deviceName    string
	redeemerPeer  string
	responderPeer string
}

// pairPeers names the two ends of an exchange so a caller cannot pass them in
// the wrong order.
type pairPeers struct {
	redeemer  string
	responder string
}

// newPairContext builds the proof context both sides derive from a hello and
// the responder's challenge, so the responder and the redeemer cannot drift in
// which fields they bind.
func newPairContext(hello pairHello, serverNonce string, peers pairPeers) pairContext {
	return pairContext{
		clientNonce:   hello.Nonce,
		serverNonce:   serverNonce,
		deviceID:      hello.DeviceID,
		deviceName:    hello.DeviceName,
		redeemerPeer:  peers.redeemer,
		responderPeer: peers.responder,
	}
}

// key stretches code, which must already be normalized, into the session key
// the proofs are MACed under. The nonces salt it, so no two exchanges share a
// key.
func (c pairContext) key(code string) ([]byte, error) {
	salt := pairTranscript("reverb-pair-kdf-v1", c.clientNonce, c.serverNonce)
	return pbkdf2.Key(sha256.New, code, salt, pairKDFIterations, pairKDFKeyBytes)
}

// proof returns the possession proof one direction of the exchange presents.
func (c pairContext) proof(key []byte, label string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(pairTranscript(label, c.clientNonce, c.serverNonce, c.deviceID, c.deviceName, c.redeemerPeer, c.responderPeer))
	return mac.Sum(nil)
}

func encodePairProof(proof []byte) string {
	return base64.RawStdEncoding.EncodeToString(proof)
}

func decodePairProof(s string) ([]byte, error) {
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	if len(b) != sha256.Size {
		return nil, fmt.Errorf("pairing proof must be a %d byte MAC", sha256.Size)
	}
	return b, nil
}
