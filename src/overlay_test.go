package main

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/httpserver"
	"github.com/apimgr/shortner/src/paths"
)

// newTestServer builds a real *httpserver.Server from a default config, so
// overlayRef's serve hooks have something concrete to call SetOverlayHost
// and ServeOverlay on rather than exercising only the nil-server no-op
// paths.
func newTestServer(t *testing.T) *httpserver.Server {
	t.Helper()
	cfg := config.Default(filepath.Join(t.TempDir(), "server.db"))
	return httpserver.New(httpserver.Options{Config: cfg})
}

// TestOverlayRefSetServer proves set/server round-trip and that server()
// starts out nil (the state before the HTTP server exists).
func TestOverlayRefSetServer(t *testing.T) {
	ref := &overlayRef{}
	if ref.server() != nil {
		t.Fatal("server() before set() = non-nil, want nil")
	}

	srv := newTestServer(t)
	ref.set(srv)
	if ref.server() != srv {
		t.Error("server() after set() did not return the published server")
	}
}

// TestOverlayRefPublishNilServer proves publish is a documented no-op, not
// a panic, when nothing has been published to the ref yet — the state
// between manager construction and the HTTP server coming up.
func TestOverlayRefPublishNilServer(t *testing.T) {
	ref := &overlayRef{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publish with a nil server panicked: %v", r)
		}
	}()
	ref.publish(httpserver.OverlayTor, "abc.onion", ln)
}

// TestOverlayRefPublishNilListener proves publish is a no-op, not a panic,
// when the manager's serve hook fires with no backend listener (a start
// that resolved an address but never got a listener, or a restart racing
// a shutdown).
func TestOverlayRefPublishNilListener(t *testing.T) {
	ref := &overlayRef{srv: newTestServer(t)}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publish with a nil listener panicked: %v", r)
		}
	}()
	ref.publish(httpserver.OverlayTor, "abc.onion", nil)
}

// TestOverlayRefClearNeverSet proves clear is safe to call on a ref whose
// set() was never called — e.g. an overlay manager that failed to start
// before the HTTP server ever published itself.
func TestOverlayRefClearNeverSet(t *testing.T) {
	ref := &overlayRef{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("clear on a never-set ref panicked: %v", r)
		}
	}()
	ref.clear(httpserver.OverlayTor)
	ref.clear(httpserver.OverlayI2P)
}

// TestOverlayRefPublishAndClearLiveServer exercises the real success path:
// a live listener gets served, then closing it out from under ServeOverlay
// (simulating the provider dying) reports through logf exactly like AI.md
// PART 31's "never fatal to the clearnet server, only reported" rule says,
// and clear() afterwards is still safe to call.
func TestOverlayRefPublishAndClearLiveServer(t *testing.T) {
	logged := make(chan string, 1)
	ref := &overlayRef{
		srv:  newTestServer(t),
		logf: func(format string, args ...any) { logged <- fmt.Sprintf(format, args...) },
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}

	ref.publish(httpserver.OverlayTor, "live.onion", ln)
	// Closing the listener out from under Serve() is exactly what a
	// provider dying looks like: ServeOverlay's Serve() returns a non-nil,
	// non-ErrServerClosed error, which the serve goroutine reports through
	// logf rather than letting escape unnoticed.
	if err := ln.Close(); err != nil {
		t.Fatalf("ln.Close: %v", err)
	}

	select {
	case msg := <-logged:
		if !strings.Contains(msg, "tor") {
			t.Errorf("logf message = %q, want it to name the tor network", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logf never fired after the backend listener closed")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("clear after a dead listener panicked: %v", r)
		}
	}()
	ref.clear(httpserver.OverlayTor)
}

// overlayTestPaths builds a Paths whose every directory lives under a
// fresh t.TempDir().
func overlayTestPaths(t *testing.T) paths.Paths {
	t.Helper()
	base := t.TempDir()
	return paths.Paths{
		Config: filepath.Join(base, "config"),
		Data:   filepath.Join(base, "data"),
		Logs:   filepath.Join(base, "logs"),
	}
}

// TestNewOverlayManagersReturnsUsableManagers proves construction never
// touches the network or filesystem: both managers come back non-nil for
// a default config, and the ref's logf is wired for later serve-loop
// errors.
func TestNewOverlayManagersReturnsUsableManagers(t *testing.T) {
	cfg := config.Default(filepath.Join(t.TempDir(), "server.db"))
	p := overlayTestPaths(t)
	ref := &overlayRef{}
	logged := false
	logf := func(string, ...any) { logged = true }

	torManager, i2pManager := newOverlayManagers(context.Background(), cfg, p, ref, logf)
	if torManager == nil {
		t.Error("newOverlayManagers: tor manager = nil, want non-nil")
	}
	if i2pManager == nil {
		t.Error("newOverlayManagers: i2p manager = nil, want non-nil")
	}
	if ref.logf == nil {
		t.Error("newOverlayManagers did not wire ref.logf")
	}
	ref.logf("probe")
	if !logged {
		t.Error("ref.logf does not call through to the supplied logf")
	}

	stopOverlays(ref, torManager, i2pManager)
}

// TestStartOverlaysDoesNotBlock proves startOverlays returns promptly (and
// never panics) when Tor has no binary to find and I2P is left at its
// opt-out default — the only overlay state this sandboxed environment can
// honestly exercise, since it has neither a tor nor an i2pd binary nor a
// live SAM bridge. Both managers fail fast rather than attempting to start
// a real process, which is exactly the "no SMTP = no email" style honest
// failure this test is checking for.
func TestStartOverlaysDoesNotBlock(t *testing.T) {
	cfg := config.Default(filepath.Join(t.TempDir(), "server.db"))
	// Force a deterministic missing-binary failure regardless of whatever
	// happens to be installed on the host running the test.
	cfg.Server.Tor.Binary = filepath.Join(t.TempDir(), "no-such-tor-binary")
	p := overlayTestPaths(t)
	ref := &overlayRef{}

	torManager, i2pManager := newOverlayManagers(context.Background(), cfg, p, ref, nil)

	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		startOverlays("shortner-test", torManager, i2pManager)
	}()

	select {
	case <-doneCh:
	case <-time.After(5 * time.Second):
		t.Fatal("startOverlays blocked for 5s with no tor binary and I2P opt-out")
	}

	stopOverlays(ref, torManager, i2pManager)
}

// TestStopOverlaysSafeWhenNothingStarted proves stopOverlays never panics
// against managers that were constructed but never started.
func TestStopOverlaysSafeWhenNothingStarted(t *testing.T) {
	cfg := config.Default(filepath.Join(t.TempDir(), "server.db"))
	p := overlayTestPaths(t)
	ref := &overlayRef{}
	torManager, i2pManager := newOverlayManagers(context.Background(), cfg, p, ref, nil)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("stopOverlays on never-started managers panicked: %v", r)
		}
	}()
	stopOverlays(ref, torManager, i2pManager)
	// Idempotent: a second stop must also be safe.
	stopOverlays(ref, torManager, i2pManager)
}
