package httpserver

import (
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func testRateLimitConfig() config.RateLimit {
	return config.RateLimit{
		Read:        config.RateLimitClass{Requests: 2, Window: 60},
		Write:       config.RateLimitClass{Requests: 1, Window: 60},
		Health:      config.RateLimitClass{Requests: 5, Window: 60},
		GlobalBurst: 10,
	}
}

func TestRateLimiterPublishesCacheMetrics(t *testing.T) {
	m := metrics.New(config.Metrics{Enabled: true})
	rl := NewRateLimiter(testRateLimitConfig())
	rl.SetMetrics(m)
	rl.Allow("192.0.2.10", rlRead)
	rl.Allow("192.0.2.10", rlRead)
	if got := testutil.ToFloat64(m.CacheMissesTotal.WithLabelValues("rate_limit")); got != 1 {
		t.Errorf("misses = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.CacheHitsTotal.WithLabelValues("rate_limit")); got != 1 {
		t.Errorf("hits = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.CacheSize.WithLabelValues("rate_limit")); got != 2 {
		t.Errorf("size = %v, want 2", got)
	}
}

func TestRateLimiterAllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter(testRateLimitConfig())

	allowed, _ := rl.Allow("1.2.3.4", rlRead)
	if !allowed {
		t.Fatal("first request should be allowed")
	}
	allowed, _ = rl.Allow("1.2.3.4", rlRead)
	if !allowed {
		t.Fatal("second request (at limit) should be allowed")
	}
}

func TestRateLimiterBlocksOverLimit(t *testing.T) {
	rl := NewRateLimiter(testRateLimitConfig())

	rl.Allow("1.2.3.4", rlRead)
	rl.Allow("1.2.3.4", rlRead)
	allowed, retryAfter := rl.Allow("1.2.3.4", rlRead)
	if allowed {
		t.Fatal("third request should be blocked (limit is 2)")
	}
	if retryAfter < 1 {
		t.Errorf("retryAfter = %d, want >= 1", retryAfter)
	}
}

func TestRateLimiterIndependentPerIP(t *testing.T) {
	rl := NewRateLimiter(testRateLimitConfig())

	rl.Allow("1.2.3.4", rlRead)
	rl.Allow("1.2.3.4", rlRead)
	allowed, _ := rl.Allow("5.6.7.8", rlRead)
	if !allowed {
		t.Error("a different IP should have its own independent window")
	}
}

func TestRateLimiterIndependentPerClass(t *testing.T) {
	rl := NewRateLimiter(testRateLimitConfig())

	rl.Allow("1.2.3.4", rlWrite)
	allowed, _ := rl.Allow("1.2.3.4", rlWrite)
	if allowed {
		t.Fatal("write class limit is 1, second write should be blocked")
	}
	allowed, _ = rl.Allow("1.2.3.4", rlRead)
	if !allowed {
		t.Error("read class should be unaffected by write class exhaustion")
	}
}

func TestRateLimiterGlobalBurst(t *testing.T) {
	cfg := config.RateLimit{
		Read:        config.RateLimitClass{Requests: 1000, Window: 60},
		Write:       config.RateLimitClass{Requests: 1000, Window: 60},
		Health:      config.RateLimitClass{Requests: 1000, Window: 60},
		GlobalBurst: 2,
	}
	rl := NewRateLimiter(cfg)

	rl.Allow("1.2.3.4", rlRead)
	rl.Allow("1.2.3.4", rlWrite)
	allowed, _ := rl.Allow("1.2.3.4", rlHealth)
	if allowed {
		t.Fatal("global burst of 2 should block the third request across classes")
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   rlClass
	}{
		{"GET", "/api/v1/items", rlRead},
		{"HEAD", "/api/v1/items", rlRead},
		{"POST", "/api/v1/items", rlWrite},
		{"DELETE", "/api/v1/items", rlWrite},
		{"GET", "/server/healthz", rlHealth},
		{"GET", "/healthz", rlHealth},
		{"GET", "/api/healthz", rlHealth},
		{"GET", "/api/v1/server/healthz", rlHealth},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			if got := classify(tt.method, tt.path); got != tt.want {
				t.Errorf("classify(%q, %q) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}
