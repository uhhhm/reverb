package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestAppNewApp(t *testing.T) {
	a := NewApp()
	if a == nil {
		t.Fatal("NewApp returned nil")
	}
	if a.GetPort() != 0 {
		t.Fatalf("initial GetPort = %d, want 0", a.GetPort())
	}
}

func TestAppOnBeforeClose(t *testing.T) {
	a := NewApp()
	if got := a.OnBeforeClose(context.Background()); got != false {
		t.Fatalf("OnBeforeClose = %v, want false", got)
	}
}

func TestAppGetPort(t *testing.T) {
	a := NewApp()
	if a.GetPort() != 0 {
		t.Fatalf("GetPort before startup = %d, want 0", a.GetPort())
	}
	a.OnStartup(context.Background())
	defer a.OnShutdown(context.Background())
	// Allow server to start.
	time.Sleep(50 * time.Millisecond)
	port := a.GetPort()
	if port == 0 {
		t.Fatal("GetPort after OnStartup = 0, want non-zero")
	}
	if port < 1 || port > 65535 {
		t.Fatalf("GetPort = %d out of range", port)
	}
	// Verify listener is actually listening.
	addr := a.ln.Addr().String()
	if addr == "" {
		t.Fatal("listener addr empty")
	}
}

func TestAppOnStartupAndShutdown(t *testing.T) {
	a := NewApp()
	ctx := context.Background()
	a.OnStartup(ctx)
	if a.GetPort() == 0 {
		t.Fatal("expected port after startup")
	}
	ln := a.ln
	if ln == nil {
		t.Fatal("expected listener after startup")
	}
	// Verify HTTP server responds (health or minimal).
	// Use api.NewServer handler fallback; app's server should be serving.
	// Give it a moment.
	time.Sleep(50 * time.Millisecond)
	addr := ln.Addr().String()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	_ = conn.Close()

	// Shutdown should close gracefully.
	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.OnShutdown(shutCtx)

	// After shutdown, port should still report previous value (not reset).
	if a.GetPort() == 0 {
		t.Fatal("GetPort after shutdown should retain port")
	}
	// Listener should be closed; dial should fail after brief wait.
	time.Sleep(50 * time.Millisecond)
	_, err = net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected dial to fail after shutdown")
	}
}

func TestAppShutdownWithoutStartup(t *testing.T) {
	a := NewApp()
	// Should not panic when shutdown without prior startup.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("OnShutdown panicked: %v", r)
		}
	}()
	a.OnShutdown(context.Background())
	if a.GetPort() != 0 {
		t.Fatalf("GetPort after shutdown without startup = %d, want 0", a.GetPort())
	}
}

func TestAppOnStartupIdempotentPort(t *testing.T) {
	a := NewApp()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	a.ln = ln
	a.srv = &http.Server{Handler: http.NewServeMux()}
	a.OnStartup(context.Background())
	defer a.OnShutdown(context.Background())
	if a.GetPort() == 0 {
		t.Fatal("expected port when listener pre-set")
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("not tcp addr")
	}
	if a.GetPort() != tcp.Port {
		t.Fatalf("GetPort = %d, want %d", a.GetPort(), tcp.Port)
	}
}
