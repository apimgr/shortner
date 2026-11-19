package netutil

import (
	"errors"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestIPv6UnavailableRecognizesPlatformErrors(t *testing.T) {
	for _, message := range []string{
		"address family not supported",
		"protocol not supported",
		"An address incompatible with the requested protocol was used",
	} {
		t.Run(message, func(t *testing.T) {
			if !ipv6Unavailable(errors.New(message)) {
				t.Fatalf("ipv6Unavailable(%q) = false", message)
			}
		})
	}
	if ipv6Unavailable(errors.New("permission denied")) {
		t.Fatal("ipv6Unavailable(permission denied) = true")
	}
}

func TestListenTCPIPv6WildcardAcceptsIPv4(t *testing.T) {
	listener, fallback, err := ListenTCP("[::]:0")
	if err != nil {
		t.Fatalf("ListenTCP([::]:0): %v", err)
	}
	defer listener.Close()
	if fallback {
		if listener.Addr().(*net.TCPAddr).IP.To4() == nil {
			t.Fatalf("fallback listener address = %v, want IPv4", listener.Addr())
		}
		return
	}

	port := listener.Addr().(*net.TCPAddr).Port
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			_ = conn.Close()
		}
		accepted <- err
	}()

	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("IPv4 connection to dual-stack listener: %v", err)
	}
	_ = conn.Close()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatalf("Accept(): %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener did not accept the IPv4 connection")
	}
}

func TestListenTCPExplicitIPv4RemainsIPv4(t *testing.T) {
	listener, fallback, err := ListenTCP("0.0.0.0:0")
	if err != nil {
		t.Fatalf("ListenTCP(0.0.0.0:0): %v", err)
	}
	defer listener.Close()
	if fallback {
		t.Fatal("explicit IPv4 address unexpectedly used a fallback")
	}
	if listener.Addr().(*net.TCPAddr).IP.To4() == nil {
		t.Fatalf("explicit IPv4 listener address = %v, want IPv4", listener.Addr())
	}
}

func TestResolveListenAddressOnlyFallsBackForIPv6Wildcard(t *testing.T) {
	resolved, fallback, err := ResolveListenAddress("[::]")
	if err != nil {
		t.Fatalf("ResolveListenAddress([::]): %v", err)
	}
	if fallback && resolved != "0.0.0.0" {
		t.Fatalf("fallback address = %q, want 0.0.0.0", resolved)
	}
	if !fallback && resolved != "[::]" {
		t.Fatalf("dual-stack address = %q, want [::]", resolved)
	}

	resolved, fallback, err = ResolveListenAddress("0.0.0.0")
	if err != nil {
		t.Fatalf("ResolveListenAddress(0.0.0.0): %v", err)
	}
	if fallback || resolved != "0.0.0.0" {
		t.Fatalf("explicit IPv4 resolved to %q (fallback=%t), want IPv4 without fallback", resolved, fallback)
	}
}
