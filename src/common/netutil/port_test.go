package netutil

import (
	"net"
	"strconv"
	"testing"
)

// GetRandomAvailablePort must return a port inside the PART 31 overlay
// range that is genuinely bindable at the moment it is returned.
func TestGetRandomAvailablePort(t *testing.T) {
	port, err := GetRandomAvailablePort()
	if err != nil {
		t.Fatalf("GetRandomAvailablePort() error = %v", err)
	}
	if port < PortRangeLow || port > PortRangeHigh {
		t.Fatalf("port %d outside range [%d, %d]", port, PortRangeLow, PortRangeHigh)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("returned port %d is not actually bindable: %v", port, err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("closing listener on port %d: %v", port, err)
	}
}

// Two independent calls should usually land on different ports; this is
// probabilistic but the ~1000-port range makes a collision improbable
// enough to trust as a smoke test of the randomization.
func TestGetRandomAvailablePortVariesAcrossCalls(t *testing.T) {
	seen := map[int]bool{}
	for i := 0; i < 5; i++ {
		port, err := GetRandomAvailablePort()
		if err != nil {
			t.Fatalf("GetRandomAvailablePort() error = %v", err)
		}
		seen[port] = true
	}
	if len(seen) < 2 {
		t.Errorf("got the same port on every call across 5 attempts: %v", seen)
	}
}

// ListenLoopback must bind on loopback within the overlay range, report the
// exact port it landed on, and the listener it returns must be the one
// actually holding that port (accept/close must work through it).
func TestListenLoopback(t *testing.T) {
	ln, port, err := ListenLoopback()
	if err != nil {
		t.Fatalf("ListenLoopback() error = %v", err)
	}
	defer ln.Close()

	if port < PortRangeLow || port > PortRangeHigh {
		t.Fatalf("port %d outside range [%d, %d]", port, PortRangeLow, PortRangeHigh)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is not a *net.TCPAddr: %T", ln.Addr())
	}
	if addr.Port != port {
		t.Errorf("listener bound port = %d, want returned port %d", addr.Port, port)
	}
	if !addr.IP.IsLoopback() {
		t.Errorf("listener bound to %v, want a loopback address", addr.IP)
	}

	// Binding a second loopback listener on the exact same port must now
	// fail, proving the first listener genuinely holds the port.
	if second, err := net.Listen("tcp", addr.String()); err == nil {
		second.Close()
		t.Errorf("expected port %d to be held by the first listener, but a second bind succeeded", port)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// After Close, the port must be free again.
	if !PortAvailable(port) {
		t.Errorf("port %d still unavailable after Close()", port)
	}
}

// ListenLoopback calls made back to back must not collide with each other
// while both listeners are still open.
func TestListenLoopbackDistinctPorts(t *testing.T) {
	ln1, port1, err := ListenLoopback()
	if err != nil {
		t.Fatalf("first ListenLoopback() error = %v", err)
	}
	defer ln1.Close()

	ln2, port2, err := ListenLoopback()
	if err != nil {
		t.Fatalf("second ListenLoopback() error = %v", err)
	}
	defer ln2.Close()

	if port1 == port2 {
		t.Errorf("two concurrent ListenLoopback() calls returned the same port %d", port1)
	}
}

// PortAvailable must report true for a genuinely free port and false for
// one that is currently held, and must not leave the probe listener open
// (a leaked probe would make the caller's real bind fail).
func TestPortAvailable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	held := ln.Addr().(*net.TCPAddr).Port
	if PortAvailable(held) {
		t.Errorf("PortAvailable(%d) = true, want false for a held port", held)
	}
	ln.Close()

	if !PortAvailable(held) {
		t.Errorf("PortAvailable(%d) = false, want true once released", held)
	}

	// The probe listener must not leak: binding the same port again right
	// after PortAvailable must succeed.
	confirm, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(held))
	if err != nil {
		t.Fatalf("PortAvailable() left port %d unusable: %v", held, err)
	}
	confirm.Close()
}

// CircuitToken must return an empty string for a nil address (the
// "connection came from nowhere identifiable" case), a stable token for
// the same address string, and different tokens for different addresses —
// and it must never simply echo the address back, since PART 31.1 forbids
// treating the synthetic PROXY-protocol source as a loggable address.
func TestCircuitToken(t *testing.T) {
	if got := CircuitToken(nil); got != "" {
		t.Errorf("CircuitToken(nil) = %q, want empty", got)
	}

	addr1 := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	addr2 := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}

	tok1a := CircuitToken(addr1)
	tok1b := CircuitToken(addr1)
	tok2 := CircuitToken(addr2)

	if tok1a == "" {
		t.Fatal("CircuitToken(addr1) returned empty string")
	}
	if tok1a != tok1b {
		t.Errorf("CircuitToken() not stable: %q != %q for the same address", tok1a, tok1b)
	}
	if tok1a == tok2 {
		t.Errorf("CircuitToken() returned the same token for two different addresses: %q", tok1a)
	}
	if tok1a == addr1.String() {
		t.Error("CircuitToken() must not return the raw address string")
	}
}
