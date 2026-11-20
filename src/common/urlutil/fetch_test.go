package urlutil

import (
	"net"
	"testing"
)

func TestPublicIPRejectsSpecialPurposeRanges(t *testing.T) {
	for _, raw := range []string{"100.100.100.200", "198.18.0.1", "192.0.2.1", "2001:db8::1", "2002::1", "3fff::1"} {
		if publicIP(net.ParseIP(raw)) {
			t.Errorf("publicIP(%q) = true, want false", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(raw)) {
			t.Errorf("publicIP(%q) = false, want true", raw)
		}
	}
}

func TestValidateRemoteURLRejectsUnsafeTargets(t *testing.T) {
	cfg := DefaultFetchRemoteImageConfig()
	for _, raw := range []string{
		"http://example.com/image.png",
		"https://localhost/image.png",
		"https://metadata.internal/image.png",
		"https://127.0.0.1/image.png",
		"https://user:password@example.com/image.png",
	} {
		t.Run(raw, func(t *testing.T) {
			if err := ValidateRemoteURL(raw, cfg); err == nil {
				t.Fatalf("ValidateRemoteURL(%q) succeeded", raw)
			}
		})
	}
}
