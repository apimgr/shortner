// Package netutil holds the small networking helpers shared by the overlay
// network packages (AI.md PART 31.1 Tor, PART 31.2 I2P).
package netutil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"net"
)

// The AI.md PART 31 "Port Allocation" range for overlay backend listeners.
// The backend port is chosen fresh on every start and is never written to
// server.yml, so an operator can never pin a value that later collides.
const (
	// PortRangeLow is the first port considered for an overlay backend.
	PortRangeLow = 64000
	// PortRangeHigh is the last port considered for an overlay backend.
	PortRangeHigh = 64999
)

// ErrNoFreePort is returned when every port in the overlay range is busy.
var ErrNoFreePort = errors.New("netutil: no free port in the overlay range")

// GetRandomAvailablePort returns a loopback-bindable TCP port from the
// AI.md PART 31 overlay range. It probes randomly rather than sequentially
// so two overlay services started at the same moment do not race onto the
// same candidate, and it verifies each candidate by actually binding it.
func GetRandomAvailablePort() (int, error) {
	span := PortRangeHigh - PortRangeLow + 1
	for attempt := 0; attempt < span*2; attempt++ {
		port := PortRangeLow + rand.Intn(span)
		if PortAvailable(port) {
			return port, nil
		}
	}
	return 0, ErrNoFreePort
}

// ListenLoopback binds a TCP listener on a random free loopback port from
// the AI.md PART 31 overlay range and returns it together with the port it
// landed on. Binding and allocating in one step is what makes the overlay
// backend port race-free: the port cannot be taken by another process
// between the availability probe and the bind.
func ListenLoopback() (net.Listener, int, error) {
	span := PortRangeHigh - PortRangeLow + 1
	for attempt := 0; attempt < span*2; attempt++ {
		port := PortRangeLow + rand.Intn(span)
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		return ln, port, nil
	}
	return nil, 0, ErrNoFreePort
}

// CircuitToken turns an overlay backend connection's source address into a
// short opaque token. Tor's `HiddenServiceExportCircuitID haproxy` encodes
// the per-rendezvous-circuit identifier into the PROXY-protocol source
// address, which is a synthetic address and never a real client IP — so it
// is hashed here rather than used directly, guaranteeing that nothing
// downstream can mistake it for, or log it as, an address.
func CircuitToken(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(addr.String()))
	return hex.EncodeToString(sum[:8])
}

// PortAvailable reports whether port can be bound on loopback right now.
// The listener is closed immediately, so this is advisory: the caller is
// expected to bind the port itself straight afterwards.
func PortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}
