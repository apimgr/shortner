//go:build windows

package netutil

import (
	"context"
	"net"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func listenIPv6Wildcard(address string) (net.Listener, error) {
	config := net.ListenConfig{Control: func(network, address string, raw syscall.RawConn) error {
		if network != "tcp6" || !isIPv6WildcardAddress(address) {
			return nil
		}
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			socketErr = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, windows.IPV6_V6ONLY, 0)
		}); err != nil {
			return err
		}
		return socketErr
	}}
	return config.Listen(context.Background(), "tcp6", address)
}

func isIPv6WildcardAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	return err == nil && strings.Trim(host, "[]") == "::"
}
