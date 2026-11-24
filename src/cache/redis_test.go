package cache

import (
	"testing"
	"time"
)

func TestRedisOptionsURLParsing(t *testing.T) {
	for _, test := range []struct {
		name     string
		url      string
		username string
		password string
		tls      bool
	}{
		{name: "valkey password only", url: "valkey://secret@cache.example.test:6379/2", password: "secret"},
		{name: "redis ACL credentials", url: "rediss://user:secret@cache.example.test:6379/1", username: "user", password: "secret", tls: true},
		{name: "TLS query option", url: "valkey://cache.example.test:6379/0?tls=true", tls: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts, err := redisOptions(RedisOptions{URL: test.url, PoolSize: 4, MinIdle: 2, Timeout: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if opts.Username != test.username || opts.Password != test.password {
				t.Errorf("credentials = (%q, %q), want (%q, %q)", opts.Username, opts.Password, test.username, test.password)
			}
			if (opts.TLSConfig != nil) != test.tls {
				t.Errorf("TLS enabled = %t, want %t", opts.TLSConfig != nil, test.tls)
			}
			if opts.PoolSize != 4 || opts.MinIdleConns != 2 || opts.DialTimeout != 3*time.Second {
				t.Errorf("pool/time options not applied: %+v", opts)
			}
		})
	}
}

func TestRedisStatsPatternScopesLiteralPrefix(t *testing.T) {
	if got, want := redisStatsPattern("shortner:tenant[1]*"), `shortner:tenant\[1\]\**`; got != want {
		t.Fatalf("redisStatsPattern() = %q, want %q", got, want)
	}
}
