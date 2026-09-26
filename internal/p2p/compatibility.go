package p2p

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"golang.org/x/mod/semver"
)

// SupportWindow is how many minor versions of each protocol a release serves
// and offers: its current one and those before it (ADR 0004). A phone updated
// later than the desktop keeps syncing while its versions are inside it.
const SupportWindow = 2

const (
	syncProtocol protocol.ID = "/reverb/sync/1.0.0"
	fileProtocol protocol.ID = "/reverb/file/1.0.0"
)

// protocolFamilies are the versioned Reverb protocols, each named by its
// oldest ID, with the minor version this release speaks. A wire change bumps
// the minor here and keeps serving the old one until it leaves the window.
// Minor 1 keeps minor 0's wire format; pairing 2.x keeps 2.0's proof
// exchange. Pairing 1.0 sent the code in plaintext and is never served.
var protocolFamilies = []struct {
	base    protocol.ID
	current int
}{
	{pairProtocol, 1},
	{syncProtocol, 1},
	{fileProtocol, 1},
	{coverProtocol, 1},
	{delegatedProtocol, 1},
	{manifestProtocol, 1},
}

// SupportedProtocols lists a family's versions inside the window, newest
// first, as a dialer offers them. Every version runs the same handler, which
// enforces the family's authentication.
func SupportedProtocols(base protocol.ID) []protocol.ID {
	for _, f := range protocolFamilies {
		if f.base != base {
			continue
		}
		prefix := strings.TrimSuffix(string(base), "0.0")
		var out []protocol.ID
		for minor := f.current; minor >= 0 && minor > f.current-SupportWindow; minor-- {
			out = append(out, protocol.ID(prefix+strconv.Itoa(minor)+".0"))
		}
		return out
	}
	return []protocol.ID{base}
}

func registerCompatible(h host.Host, base protocol.ID, handler network.StreamHandler) {
	for _, p := range SupportedProtocols(base) {
		h.SetStreamHandler(p, handler)
	}
}

// Compatibility is whether a paired device shares a version of every protocol.
type Compatibility string

const (
	Compatible   Compatibility = "compatible"
	Incompatible Compatibility = "incompatible"
	// CompatibilityUnknown is a device not reached since this device started.
	CompatibilityUnknown Compatibility = "unknown"
)

// PeerVersion is what a paired device announced when last reached: its build
// version, and whether its protocols overlap this device's window.
type PeerVersion struct {
	PeerID        string        `json:"peerId"`
	DeviceID      string        `json:"deviceId"`
	Version       string        `json:"version"`
	Compatibility Compatibility `json:"compatibility"`
	// Message tells the owner which device to update when incompatible.
	Message string `json:"message"`
}

// VersionOf reports a paired device from what libp2p's identify exchange
// recorded. local is this device's build version, which says which of the
// two is older.
func VersionOf(h host.Host, pid peer.ID, deviceID, local string) PeerVersion {
	v := PeerVersion{PeerID: pid.String(), DeviceID: deviceID, Compatibility: CompatibilityUnknown}
	if agent, err := h.Peerstore().Get(pid, "AgentVersion"); err == nil {
		if s, ok := agent.(string); ok {
			v.Version = strings.TrimPrefix(s, "Reverb/")
		}
	}
	protocols, err := h.Peerstore().GetProtocols(pid)
	if err != nil || len(protocols) == 0 {
		return v
	}
	available := map[protocol.ID]bool{}
	for _, p := range protocols {
		available[p] = true
	}
	for _, f := range protocolFamilies {
		shared := false
		for _, p := range SupportedProtocols(f.base) {
			shared = shared || available[p]
		}
		if !shared {
			v.Compatibility = Incompatible
			v.Message = fmt.Sprintf("This device and %s are too many releases apart to share %s. %s",
				otherDevice(v.Version), strings.Split(string(f.base), "/")[2], whichToUpdate(local, v.Version))
			return v
		}
	}
	v.Compatibility = Compatible
	return v
}

func otherDevice(version string) string {
	if version == "" {
		return "the other device"
	}
	return "the other device (Reverb " + version + ")"
}

// whichToUpdate names the older of two build versions, when both are releases.
func whichToUpdate(local, peer string) string {
	l, p := "v"+strings.TrimPrefix(local, "v"), "v"+strings.TrimPrefix(peer, "v")
	switch {
	case !semver.IsValid(l) || !semver.IsValid(p):
		return "Update Reverb on whichever device is older."
	case semver.Compare(l, p) < 0:
		return "Update Reverb on this device."
	default:
		return "Update Reverb on the other device."
	}
}
