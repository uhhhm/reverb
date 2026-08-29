package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/uhhhm/reverb/internal/api"
	"github.com/uhhhm/reverb/internal/app"
	"github.com/uhhhm/reverb/internal/config"
)

// main is the server entry point. Everything except listening and shutdown is
// built by internal/app, which the desktop build shares.
func main() {
	log.Printf("reverb %s starting", version)

	// Root context cancelled when main returns, so background goroutines (e.g. the
	// playlist-sync scheduler) shut down with the process.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.Load(os.Args[1:], os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	// The HTTP API has no authentication: every request is served as the
	// household owner, so the bind address is the whole access boundary. Refuse
	// a non-loopback bind unless the operator has said so explicitly, so the
	// safe configuration is the one that requires no thought.
	if !isLoopbackAddr(cfg.BindAddr) && !cfg.AllowNetworkAccess {
		log.Fatalf("refusing to bind %s: Reverb authenticates every request as the household "+
			"owner, so anyone who can reach this port has full access to your library, "+
			"settings and downloads. Bind %s instead, or pass --allow-network-access "+
			"(REVERB_ALLOW_NETWORK_ACCESS=1) if an authenticating proxy fronts it. "+
			"Device-to-device sync does not need this: it runs over libp2p on its own "+
			"port and admits only paired peers.", cfg.BindAddr, config.DefaultBindAddr)
	}

	rt, err := app.Build(ctx, app.Options{
		DBPath:     cfg.DBPath,
		Version:    version,
		UpdateRepo: cfg.UpdateRepo,
		P2PPort:    cfg.P2PPort,
		Dev:        cfg.Dev,
		Getenv:     os.Getenv,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	rt.StartBackground(ctx)

	addr := net.JoinHostPort(cfg.BindAddr, strconv.Itoa(cfg.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("reverb listening on %s (dev=%v)", addr, cfg.Dev)
	if !isLoopbackAddr(cfg.BindAddr) {
		log.Printf("WARNING: bound to %s with --allow-network-access. The API has no "+
			"authentication of its own; anyone who can reach this port has full access. "+
			"Only do this behind a proxy that authenticates for it.", cfg.BindAddr)
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; close(stop) }()

	httpSrv := newHTTPServer(api.NewServer(rt.Deps).Handler())
	if err := serveWithShutdown(httpSrv, ln, stop, func(ctx context.Context) error {
		if rt.Bundle.Supervisor != nil {
			return rt.Bundle.Supervisor.Shutdown(ctx)
		}
		return nil
	}); err != nil {
		log.Fatal(err)
	}
}

// isLoopbackAddr reports whether addr keeps the listener off the network.
// The empty string and ":port" form bind every interface, so they are not.
func isLoopbackAddr(addr string) bool {
	if addr == "" {
		return false
	}
	// A literal IPv6 address may still arrive bracketed from a caller that did
	// not go through config.Load; unwrap the pair rather than trimming stray
	// brackets off either end.
	if len(addr) >= 2 && strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		addr = addr[1 : len(addr)-1]
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.IsLoopback()
	}
	// A hostname: only the conventional loopback names are treated as safe.
	return strings.EqualFold(addr, "localhost")
}
