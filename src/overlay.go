// Startup wiring for the AI.md PART 31 overlay networks: the Tor hidden
// service (31.1, always attempted) and the I2P eepsite (31.2, opt-in).
//
// Both managers are built before the HTTP server so the scheduler's
// tor_health/i2p_health tasks and /server/healthz can see them, and both
// are started after it so their backend listeners have a handler to serve.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/httpserver"
	"github.com/apimgr/shortner/src/i2p"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/tor"
)

// overlayRef holds the running HTTP server for the overlay managers'
// serve hooks. It exists because a manager is constructed before the
// server (the server reports the managers' state at /server/healthz), yet
// its serve hook only ever fires after the server has been published here.
type overlayRef struct {
	mu  sync.Mutex
	srv *httpserver.Server
	// logf receives serve-loop errors. Overlay failures are never fatal to
	// the clearnet server, so they are reported and nothing more.
	logf func(string, ...any)
}

// set publishes the server the serve hooks will use.
func (o *overlayRef) set(srv *httpserver.Server) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.srv = srv
}

// server returns the published server, or nil when nothing has been
// published yet.
func (o *overlayRef) server() *httpserver.Server {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.srv
}

// publish records the overlay's live address and begins serving its
// backend listener. A restart hands over a brand new listener on a new
// port, so this runs on every successful start of either provider.
func (o *overlayRef) publish(network httpserver.OverlayNetwork, address string, ln net.Listener) {
	srv := o.server()
	if srv == nil || ln == nil {
		return
	}
	srv.SetOverlayHost(network, address)
	go func() {
		if err := srv.ServeOverlay(ln, network); err != nil && o.logf != nil {
			o.logf("%s: backend listener stopped: %v", network, err)
		}
	}()
}

// clear drops the overlay's address so nothing keeps advertising a
// service that is no longer running (Onion-Location, the footer block, the
// help page and the {onion_address} template variable all read it).
func (o *overlayRef) clear(network httpserver.OverlayNetwork) {
	if srv := o.server(); srv != nil {
		srv.SetOverlayHost(network, "")
	}
}

// newOverlayManagers builds the Tor and I2P managers. Neither one touches
// the network or the filesystem here — that happens in startOverlays, once
// the HTTP server exists.
func newOverlayManagers(ctx context.Context, cfg *config.Config, p paths.Paths, ref *overlayRef, logf func(string, ...any)) (*tor.Manager, *i2p.Manager) {
	ref.logf = logf

	torDirs := tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}
	torManager := tor.NewManager(ctx, cfg.Server.Tor, torDirs, func(svc *tor.Service) {
		ref.publish(httpserver.OverlayTor, svc.OnionAddress(), svc.BackendListener())
	}, logf)

	i2pDirs := i2p.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}
	i2pManager := i2p.NewManager(ctx, cfg.Server.I2P, i2pDirs, func(svc *i2p.Service) {
		ref.publish(httpserver.OverlayI2P, svc.EepsiteAddress(), svc.BackendListener())
	}, logf)

	return torManager, i2pManager
}

// startOverlays brings both networks up. Every failure here is an INFO or
// warning, never fatal: AI.md PART 31.1 makes a missing `tor` binary a
// normal, supported state, and PART 31.2 leaves I2P switched off unless
// the operator opted in.
func startOverlays(binaryName string, torManager *tor.Manager, i2pManager *i2p.Manager) {
	if err := torManager.Start(); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": tor: "+err.Error())
	}
	if err := i2pManager.Start(); err != nil && err != i2p.ErrDisabled {
		fmt.Fprintln(os.Stderr, binaryName+": i2p: "+err.Error())
	}
}

// stopOverlays closes both managers and stops advertising their addresses.
func stopOverlays(ref *overlayRef, torManager *tor.Manager, i2pManager *i2p.Manager) {
	ref.clear(httpserver.OverlayTor)
	ref.clear(httpserver.OverlayI2P)
	_ = torManager.Close()
	_ = i2pManager.Close()
}

// startupURLs builds the address list for the AI.md PART 20 startup banner:
// the clearnet URL first, then the overlay rows from PART 31's banner
// examples (`Tor: http://{onion_address}`, `I2P: http://{i2p_address}`).
//
// Overlay addresses are read from disk rather than from the live managers
// because bootstrap runs in the background and can take minutes; a
// previously bootstrapped instance already has its persisted hostname, so
// the row appears from the second start onwards. When no address is known
// yet the row is omitted entirely and the bootstrap success line prints it
// as soon as the network answers — never a placeholder, never a guess.
func startupURLs(cfg *config.Config, p paths.Paths, scheme, host string) []string {
	urls := []string{fmt.Sprintf("%s://%s:%s%s", scheme, host, cfg.Server.Port, cfg.Server.BaseURL)}
	if onion := tor.ReadOnionAddress(p.Data); onion != "" {
		urls = append(urls, "Tor: http://"+onion)
	}
	if cfg.Server.I2P.Enabled {
		if address := i2p.ReadEepsiteAddress(p.Data); address != "" {
			urls = append(urls, "I2P: http://"+address)
		}
	}
	return urls
}
