package httpserver

import (
	"testing"

	"github.com/apimgr/shortner/src/config"
)

func testRateLimitConfig() config.RateLimit {
	return config.RateLimit{
		Enabled:     true,
		Read:        config.RateLimitClass{Requests: 2, Window: 60},
		Write:       config.RateLimitClass{Requests: 1, Window: 60},
		Health:      config.RateLimitClass{Requests: 5, Window: 60},
		GlobalBurst: 10,
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

func TestRateLimiterDisabledAlwaysAllows(t *testing.T) {
	cfg := testRateLimitConfig()
	cfg.Enabled = false
	rl := NewRateLimiter(cfg)

	for i := 0; i < 100; i++ {
		allowed, _ := rl.Allow("1.2.3.4", rlRead)
		if !allowed {
			t.Fatal("disabled rate limiter should always allow")
		}
	}
}

func TestRateLimiterGlobalBurst(t *testing.T) {
	cfg := config.RateLimit{
		Enabled:     true,
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
