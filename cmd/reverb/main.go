package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
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

	rt, err := app.Build(ctx, app.Options{
		DBPath:     cfg.DBPath,
		Version:    version,
		UpdateRepo: cfg.UpdateRepo,
		Dev:        cfg.Dev,
		Getenv:     os.Getenv,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer rt.Close()

	rt.StartBackground(ctx)

	addr := fmt.Sprintf(":%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("reverb listening on %s (dev=%v)", addr, cfg.Dev)

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
