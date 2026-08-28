package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// PairingService is the minimal seam needed for p2p pairing.
// *sync.PairingService satisfies it.
type PairingService interface {
	GenerateCode(ctx context.Context) (string, int64, error)
	Redeem(ctx context.Context, rawCode, deviceName string) (string, string, error)
}

// pairRequest mirrors api.redeemRequest for libp2p.
type pairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"deviceName"`
}

type pairResponse struct {
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
	Error    string `json:"error,omitempty"`
}

// RegisterPairingHandler mounts /reverb/pair/1.0.0 on the host. The handler
// calls pairing.Redeem and streams back JSON. Single-code UX: generator
// shows XXXX-XXXX, redeemer dials generator via relay/mDNS and sends this.
func RegisterPairingHandler(h host.Host, pairing PairingService) {
	h.SetStreamHandler("/reverb/pair/1.0.0", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(10 * time.Second))
		var req pairRequest
		if err := json.NewDecoder(s).Decode(&req); err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: fmt.Sprintf("decode: %v", err)})
			return
		}
		deviceID, token, err := pairing.Redeem(context.Background(), req.Code, req.DeviceName)
		if err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: err.Error()})
			return
		}
		_ = json.NewEncoder(s).Encode(pairResponse{DeviceID: deviceID, Token: token})
	})
}

// RedeemViaPeer dials peerID via the host and redeems a pairing code over libp2p.
// It is the remote counterpart to HTTP POST /pairing/redeem. It requires the
// peer to be discoverable via mDNS or have a known multiaddr in the peerstore
// (via relay). For LAN, mDNS will have already connected the hosts.
func RedeemViaPeer(ctx context.Context, h host.Host, peerIDStr string, code, deviceName string) (string, string, error) {
	if h == nil {
		return "", "", fmt.Errorf("host is nil")
	}
	pid, err := peer.Decode(peerIDStr)
	if err != nil {
		return "", "", err
	}
	s, err := h.NewStream(ctx, pid, "/reverb/pair/1.0.0")
	if err != nil {
		return "", "", fmt.Errorf("open stream: %w", err)
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(s).Encode(pairRequest{Code: code, DeviceName: deviceName}); err != nil {
		return "", "", err
	}
	// Close write side to signal EOF for some transports.
	_ = s.CloseWrite()
	var resp pairResponse
	if err := json.NewDecoder(s).Decode(&resp); err != nil {
		return "", "", err
	}
	if resp.Error != "" {
		return "", "", fmt.Errorf("%s", resp.Error)
	}
	if resp.DeviceID == "" || resp.Token == "" {
		return "", "", fmt.Errorf("invalid pair response")
	}
	return resp.DeviceID, resp.Token, nil
}
