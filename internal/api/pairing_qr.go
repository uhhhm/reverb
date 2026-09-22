package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/uhhhm/reverb/internal/p2p"
	"github.com/uhhhm/reverb/internal/sync"
)

// pairingQR is the QR form of a freshly minted pairing code: the payload a
// phone scans, and the same payload drawn as an SVG for the pairing screen.
// Both are empty when this device has no address another could dial, since a
// payload with nowhere to dial would be useless.
func (s *Server) pairingQR(code string, expiresAt int64) (payload, svg string) {
	h := s.p2pHost()
	if h == nil {
		return "", ""
	}
	payload, err := p2p.EncodePairPayload(p2p.PairPayload{Code: code, ExpiresAt: expiresAt, Addrs: h.DialAddrs()})
	if err != nil {
		return "", ""
	}
	if svg, err = qrSVG(payload); err != nil {
		return payload, ""
	}
	return payload, svg
}

// qrSVG draws text as a QR code: one path of unit squares on a white field
// with the four-module quiet zone scanners expect. Medium error correction
// leaves room for a smudged screen without making the code too dense to scan
// from across a desk.
func qrSVG(text string) (string, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	const quiet = 4
	n := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}

type p2pRedeemQRRequest struct {
	Payload    string `json:"payload"`
	DeviceName string `json:"deviceName"`
}

// handleP2PRedeemQR pairs with the device whose pairing QR code was scanned.
// It dials the addresses the payload carries rather than relying on discovery,
// and proves possession of the code exactly as a typed redeem does.
func (s *Server) handleP2PRedeemQR(w http.ResponseWriter, r *http.Request) {
	h := s.p2pHost()
	if h == nil || h.LibHost() == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "p2p unavailable"})
		return
	}
	var body p2pRedeemQRRequest
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Payload) == "" || strings.TrimSpace(body.DeviceName) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload and deviceName are required"})
		return
	}
	// Unauthenticated like the typed redeem, so it needs the same
	// brute-force bound over the same secret.
	limiterKey := pairingClientKey(r)
	if !p2p.AllowPairAttempt(limiterKey) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many pairing attempts; try again later"})
		return
	}
	payload, target, err := p2p.ParsePairPayload(body.Payload, time.Now())
	switch {
	case errors.Is(err, p2p.ErrPairPayloadExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var guard *p2p.Guard
	if s.deps.P2PGuard != nil {
		guard = s.deps.P2PGuard()
	}
	if guard == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "peer trust store unavailable"})
		return
	}
	deviceID, token, err := p2p.RedeemViaAddrs(r.Context(), h.LibHost(), guard, s.deps.DeviceKeys, target,
		payload.Code, strings.TrimSpace(body.DeviceName), s.localSyncDeviceID(r.Context()))
	if err != nil {
		switch {
		case errors.Is(err, p2p.ErrPairingRateLimited):
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		case errors.Is(err, sync.ErrCodeExpired):
			writeJSON(w, http.StatusGone, map[string]string{"error": err.Error()})
		case errors.Is(err, sync.ErrCodeUsed):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, sync.ErrCodeInvalid), errors.Is(err, p2p.ErrPairingProofInvalid):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	p2p.ResetPairAttempts(limiterKey)
	writeJSON(w, http.StatusOK, map[string]string{"deviceId": deviceID, "token": token})
}
