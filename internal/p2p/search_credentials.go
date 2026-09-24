package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Search credentials travel only over a trusted libp2p connection (Noise),
// never through replicated state or the phone's loopback HTTP API.
const searchCredentialsProtocol = "/reverb/search-credentials/1.0.0"

// SearchCredentials are a Spotify app's client credentials, which the phone
// needs to search Spotify without a desktop in reach.
type SearchCredentials struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type searchCredentialsResponse struct {
	Credentials SearchCredentials `json:"credentials"`
	Available   bool              `json:"available"`
}

// RegisterSearchCredentialsHandler serves the configured owner's Spotify
// credentials only after the peer has passed the same trust guard as sync.
// provide returns ErrNoSearchCredentials when Spotify is not configured.
func RegisterSearchCredentialsHandler(h host.Host, guard *Guard, provide func(context.Context) (SearchCredentials, error)) {
	h.SetStreamHandler(searchCredentialsProtocol, safeHandler("search-credentials", func(s network.Stream) {
		defer s.Close()
		_ = s.SetDeadline(time.Now().Add(10 * time.Second))
		if guard == nil || provide == nil {
			_ = s.Reset()
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, rejected := guard.rejectUntrusted(ctx, s); rejected {
			return
		}
		credentials, err := provide(ctx)
		if errors.Is(err, ErrNoSearchCredentials) {
			_ = json.NewEncoder(s).Encode(searchCredentialsResponse{})
			return
		}
		if err != nil {
			// A failure is not "not configured": the phone must keep its copy.
			_ = s.Reset()
			return
		}
		_ = json.NewEncoder(s).Encode(searchCredentialsResponse{
			Credentials: credentials,
			Available:   credentials.ClientID != "" && credentials.ClientSecret != "",
		})
	}))
}

// ErrNoSearchCredentials means the peer answered but has no enabled Spotify
// source with both credentials configured.
var ErrNoSearchCredentials = errors.New("paired device has no Spotify credentials")

// RequestSearchCredentials asks pid for its Spotify credentials. pid's handler
// applies the pairing trust guard before answering.
func RequestSearchCredentials(ctx context.Context, h host.Host, pid peer.ID) (SearchCredentials, error) {
	if h == nil {
		return SearchCredentials{}, errors.New("search credential transport unavailable")
	}
	s, err := h.NewStream(ctx, pid, searchCredentialsProtocol)
	if err != nil {
		return SearchCredentials{}, err
	}
	defer s.Close()
	_ = s.SetDeadline(time.Now().Add(10 * time.Second))
	_ = s.CloseWrite()
	var response searchCredentialsResponse
	if err := decodeLimited(s, 4096, &response); err != nil {
		return SearchCredentials{}, err
	}
	if !response.Available || response.Credentials.ClientID == "" || response.Credentials.ClientSecret == "" {
		return SearchCredentials{}, ErrNoSearchCredentials
	}
	return response.Credentials, nil
}

// CopySearchCredentials tries the server first and then other paired devices.
// The caller must keep the result in platform secure storage, not the sync DB.
// ErrNoSearchCredentials means every paired device answered without any, or
// none is paired, so a previously copied secret is stale. Other errors,
// including any device left unreachable, leave the copy undecided.
func (d *Delegator) CopySearchCredentials(ctx context.Context) (SearchCredentials, error) {
	var credentials SearchCredentials
	err := d.eachPeer(ctx, func(h host.Host, pid peer.ID) error {
		var err error
		credentials, err = RequestSearchCredentials(ctx, h, pid)
		return err
	})
	switch {
	case err == nil:
		return credentials, nil
	case errors.Is(err, errNoPairedPeer), onlyNoSearchCredentials(err):
		return SearchCredentials{}, ErrNoSearchCredentials
	default:
		// %v, not %w: a mixed failure must not read as ErrNoSearchCredentials.
		return SearchCredentials{}, fmt.Errorf("no paired device with Spotify credentials is reachable: %v", err)
	}
}

// onlyNoSearchCredentials reports whether every device's failure in err is
// ErrNoSearchCredentials.
func onlyNoSearchCredentials(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range joined.Unwrap() {
			if !errors.Is(e, ErrNoSearchCredentials) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, ErrNoSearchCredentials)
}
