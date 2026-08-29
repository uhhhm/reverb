package config

import (
	"flag"
	"io"
	"strconv"
	"strings"
)

type Config struct {
	Port int
	// BindAddr is the interface the HTTP listener binds to. It defaults to
	// loopback: Reverb authenticates every local request as the household
	// owner, so a listener reachable from the network hands that identity to
	// anyone who can route to it. Widen it only behind a fronting proxy that
	// does its own authentication, or inside a container whose port mapping is
	// the deliberate exposure decision.
	BindAddr string
	DBPath   string
	// P2PPort is the fixed libp2p listen port. It is fixed rather than random
	// so that a peer address entered on another device stays valid across
	// restarts -- the only way to reach a peer on a VPN, where mDNS multicast
	// does not cross the tunnel. 0 asks libp2p for a random port.
	P2PPort int
	Dev     bool
	// UpdateRepo is the GitHub "owner/name" polled for releases. Empty
	// disables update checks entirely (REVERB_UPDATE_REPO=off).
	UpdateRepo string
}

// DefaultUpdateRepo is the repository update checks use when
// REVERB_UPDATE_REPO is unset.
const DefaultUpdateRepo = "uhhhm/reverb"

// DefaultP2PPort is the libp2p listen port when none is configured.
const DefaultP2PPort = 4331

// DefaultBindAddr keeps the listener on loopback unless explicitly widened.
const DefaultBindAddr = "127.0.0.1"

// Load resolves config: flags win over env, env wins over defaults.
func Load(args []string, getenv func(string) string) (Config, error) {
	c := Config{Port: 8090, BindAddr: DefaultBindAddr, DBPath: "./data/reverb.db", P2PPort: DefaultP2PPort, UpdateRepo: DefaultUpdateRepo}

	if v := getenv("REVERB_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.Port = p
		}
	}
	if v := getenv("REVERB_BIND"); v != "" {
		c.BindAddr = v
	}
	if v := getenv("REVERB_P2P_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p >= 0 && p <= 65535 {
			c.P2PPort = p
		}
	}
	if v := getenv("REVERB_DB"); v != "" {
		c.DBPath = v
	}
	if getenv("REVERB_DEV") == "1" {
		c.Dev = true
	}
	if v := getenv("REVERB_UPDATE_REPO"); v != "" {
		c.UpdateRepo = v
	}

	fs := flag.NewFlagSet("reverb", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.IntVar(&c.Port, "port", c.Port, "HTTP port")
	fs.StringVar(&c.BindAddr, "bind", c.BindAddr, "interface to bind (0.0.0.0 exposes Reverb to the network)")
	fs.StringVar(&c.DBPath, "db", c.DBPath, "SQLite path")
	fs.IntVar(&c.P2PPort, "p2p-port", c.P2PPort, "libp2p listen port (0 picks a random port)")
	fs.BoolVar(&c.Dev, "dev", c.Dev, "dev mode (proxy Vite)")
	fs.StringVar(&c.UpdateRepo, "update-repo", c.UpdateRepo, `GitHub owner/name to check for updates ("off" disables)`)
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if c.UpdateRepo == "off" {
		c.UpdateRepo = ""
	}
	c.BindAddr = normalizeBindAddr(c.BindAddr)
	return c, nil
}

// normalizeBindAddr strips the brackets from a literal IPv6 host. Users write
// "[::1]" because that is how the address appears in a host:port string, but
// net.JoinHostPort re-brackets any host containing a colon, so passing the
// bracketed form through would produce "[[::1]]:8090" and fail to listen.
func normalizeBindAddr(addr string) string {
	if len(addr) >= 2 && strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		return addr[1 : len(addr)-1]
	}
	return addr
}
