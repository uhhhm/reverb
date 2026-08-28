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
	// PeerDeviceID is the responder's own device ID. The redeemer records it
	// against the responder's peer ID so it can later verify which device that
	// peer is entitled to speak for.
	PeerDeviceID string `json:"peerDeviceId,omitempty"`
	Error        string `json:"error,omitempty"`
}

// pairLimiter throttles pairing attempts per remote peer and globally. Pairing
// is necessarily reachable by unpaired peers, so it is the one handler that
// cannot be gated by Guard and needs its own brute-force bound.
var pairLimiter = newAttemptLimiter(pairAttemptsPerPeer, pairAttemptsGlobal, pairAttemptWindow)

// RegisterPairingHandler mounts /reverb/pair/1.0.0 on the host. This is the
// only handler open to unpaired peers — it is how trust is bootstrapped — so it
// is rate limited and binds the caller's libp2p peer ID to the device row it
// creates. localDeviceID supplies this node's own device ID for the response;
// it may be nil, in which case the peer cannot bind us to a device.
func RegisterPairingHandler(h host.Host, pairing PairingService, guard *Guard, localDeviceID func(context.Context) (string, error)) {
	h.SetStreamHandler("/reverb/pair/1.0.0", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(10 * time.Second))
		remote := s.Conn().RemotePeer()
		if !pairLimiter.Allow(remote.String()) {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: "too many pairing attempts; try again later"})
			return
		}
		var req pairRequest
		if err := decodeLimited(s, maxPairRequestBytes, &req); err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: fmt.Sprintf("decode: %v", err)})
			return
		}
		ctx := context.Background()
		deviceID, token, err := pairing.Redeem(ctx, req.Code, req.DeviceName)
		if err != nil {
			_ = json.NewEncoder(s).Encode(pairResponse{Error: err.Error()})
			return
		}
		// Bind the authenticated libp2p identity to the device just created.
		// Without this the sync and file handlers have no way to tell who is
		// calling, and the device ID alone would be the only credential.
		if guard != nil {
			if err := guard.Trust(ctx, remote, deviceID, req.DeviceName); err != nil {
				_ = json.NewEncoder(s).Encode(pairResponse{Error: fmt.Sprintf("trust peer: %v", err)})
				return
			}
		}
		pairLimiter.Reset(remote.String())
		resp := pairResponse{DeviceID: deviceID, Token: token}
		if localDeviceID != nil {
			if id, err := localDeviceID(ctx); err == nil {
				resp.PeerDeviceID = id
			}
		}
		_ = json.NewEncoder(s).Encode(resp)
	})
}

// RedeemViaPeer dials peerID via the host and redeems a pairing code over libp2p.
// It is the remote counterpart to HTTP POST /pairing/redeem. It requires the
// peer to be discoverable via mDNS or have a known multiaddr in the peerstore
// (via relay). For LAN, mDNS will have already connected the hosts.
//
// On success the responding peer is added to the local trust set, bound to the
// device ID it reported for itself. This is the redeemer half of the mutual
// binding the pairing handler performs on the other side.
func RedeemViaPeer(ctx context.Context, h host.Host, guard *Guard, peerIDStr, code, deviceName string) (string, string, error) {
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
	if err := decodeLimited(s, maxPairRequestBytes, &resp); err != nil {
		return "", "", err
	}
	if resp.Error != "" {
		return "", "", fmt.Errorf("%s", resp.Error)
	}
	if resp.DeviceID == "" || resp.Token == "" {
		return "", "", fmt.Errorf("invalid pair response")
	}
	if guard != nil {
		if err := guard.Trust(ctx, pid, resp.PeerDeviceID, deviceName); err != nil {
			return "", "", fmt.Errorf("trust peer: %w", err)
		}
	}
	return resp.DeviceID, resp.Token, nil
}
