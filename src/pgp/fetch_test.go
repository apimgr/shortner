package pgp

import (
	"context"
	"strings"
	"testing"
)

func TestFetchPublicKeyRejectsUnsafeURLs(t *testing.T) {
	for _, raw := range []string{
		"http://example.test/key.asc",
		"https://127.0.0.1/key.asc",
		"https://[::1]/key.asc",
		"https://[::ffff:127.0.0.1]/key.asc",
		"https://user@example.test/key.asc",
		"https://example.test/key.asc#fragment",
	} {
		t.Run(strings.ReplaceAll(raw, "/", "_"), func(t *testing.T) {
			if _, err := FetchPublicKey(context.Background(), raw); err == nil {
				t.Fatalf("FetchPublicKey(%q) succeeded for an unsafe URL", raw)
			}
		})
	}
}
