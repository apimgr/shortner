package netutil

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// ListenTCP binds one address without widening an explicit interface choice.
// An IPv6 wildcard is dual-stack; it falls back to IPv4 only when IPv6 is
// unavailable on the host.
func ListenTCP(address string) (net.Listener, bool, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, false, fmt.Errorf("netutil: invalid listen address %q: %w", address, err)
	}
	host = strings.Trim(host, "[]")
	if host == "::" {
		listener, err := listenIPv6Wildcard(address)
		if err == nil {
			return listener, false, nil
		}
		if !ipv6Unavailable(err) {
			return nil, false, err
		}
		fallbackConfig := net.ListenConfig{}
		fallback, fallbackErr := fallbackConfig.Listen(context.Background(), "tcp4", net.JoinHostPort("0.0.0.0", port))
		if fallbackErr != nil {
			return nil, true, fmt.Errorf("netutil: IPv6 unavailable (%v), IPv4 fallback failed: %w", err, fallbackErr)
		}
		return fallback, true, nil
	}

	network := "tcp"
	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			network = "tcp4"
		} else {
			network = "tcp6"
		}
	}
	config := net.ListenConfig{}
	listener, err := config.Listen(context.Background(), network, address)
	if err != nil {
		return nil, false, err
	}
	return listener, false, nil
}

// ResolveListenAddress checks IPv6 wildcard availability before startup binds
// privileged ports or drops privileges. It lets every listener use the same
// resolved address for the lifetime of the process.
func ResolveListenAddress(address string) (string, bool, error) {
	host, _, err := net.SplitHostPort(net.JoinHostPort(strings.Trim(address, "[]"), "0"))
	if err != nil {
		return "", false, err
	}
	if host != "::" {
		return address, false, nil
	}
	listener, fallback, err := ListenTCP(net.JoinHostPort(host, "0"))
	if err != nil {
		return "", false, err
	}
	if closeErr := listener.Close(); closeErr != nil {
		return "", false, closeErr
	}
	if fallback {
		return "0.0.0.0", true, nil
	}
	return address, false, nil
}

func ipv6Unavailable(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address family not supported") ||
		strings.Contains(message, "protocol not supported") ||
		strings.Contains(message, "address incompatible with the requested protocol")
}
