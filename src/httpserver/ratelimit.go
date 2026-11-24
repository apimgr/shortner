// Per-IP sliding-window rate limiter, per AI.md PART 12 "Rate Limiting".
// Redis/Valkey backends use atomic shared counters; other cache backends use
// a process-local sliding-window map.
package httpserver

import (
	"log"
	"sync"
	"time"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/metrics"
)

type rateCounter interface {
	AllowRate(classKey, ip string, classLimit, globalLimit int, classWindow, globalWindow time.Duration) (bool, int, bool)
}

// rlClass identifies which limit tier a request belongs to.
type rlClass int

const (
	rlRead rlClass = iota
	rlWrite
	rlHealth
)

// String names the class for metrics labels (AI.md PART 20
// "Rate Limit Metrics" -> limit label).
func (c rlClass) String() string {
	switch c {
	case rlWrite:
		return "write"
	case rlHealth:
		return "health"
	default:
		return "read"
	}
}

// window tracks one sliding-window counter: the count of requests seen
// since windowStart, reset once the configured window elapses.
type window struct {
	count       int
	windowStart time.Time
}

// rlSweepInterval is how often Allow prunes windows whose period has
// already elapsed. Without it the per-IP maps grow without bound — every
// distinct source address ever seen would be retained forever, which is a
// remotely triggerable memory-exhaustion vector.
const rlSweepInterval = time.Minute

// RateLimiter implements AI.md PART 12 "Rate Limiting": independent
// per-class per-IP sliding windows, plus a global-burst ceiling per IP
// across all classes.
type RateLimiter struct {
	mu           sync.Mutex
	cfg          config.RateLimit
	byClass      map[rlClass]map[string]*window
	global       map[string]*window
	lastSweep    time.Time
	backend      rateCounter
	metrics      *metrics.Metrics
	fallbackWarn sync.Once
}

// NewRateLimiter builds a RateLimiter from server.yml's rate_limit config.
func NewRateLimiter(cfg config.RateLimit, backend ...rateCounter) *RateLimiter {
	rl := &RateLimiter{
		cfg: cfg,
		byClass: map[rlClass]map[string]*window{
			rlRead:   {},
			rlWrite:  {},
			rlHealth: {},
		},
		global: map[string]*window{},
	}
	if len(backend) > 0 {
		rl.backend = backend[0]
	}
	return rl
}

// SetMetrics connects the limiter's in-process counters to PART 20 cache metrics.
func (rl *RateLimiter) SetMetrics(m *metrics.Metrics) { rl.metrics = m }

// classLimit returns the configured (requests, window) for class.
func (rl *RateLimiter) classLimit(class rlClass) config.RateLimitClass {
	switch class {
	case rlWrite:
		return rl.cfg.Write
	case rlHealth:
		return rl.cfg.Health
	default:
		return rl.cfg.Read
	}
}

// Allow reports whether ip may proceed for class, and if not, the number of
// seconds until the window resets (for the Retry-After header).
func (rl *RateLimiter) Allow(ip string, class rlClass) (allowed bool, retryAfterSeconds int) {
	limit := rl.classLimit(class)
	if rl.backend != nil {
		if allowed, retry, decided := rl.backend.AllowRate(class.String()+":"+ip, ip, limit.Requests, rl.cfg.GlobalBurst, time.Duration(limit.Window)*time.Second, time.Minute); decided {
			return allowed, retry
		}
		rl.fallbackWarn.Do(func() { log.Printf("cache: external rate-limit counter unavailable; using in-process counters") })
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.sweepLocked(now)

	classWindows := rl.byClass[class]
	w, ok := classWindows[ip]
	hit := ok && now.Sub(w.windowStart) < time.Duration(limit.Window)*time.Second
	if globalWindow, found := rl.global[ip]; found && now.Sub(globalWindow.windowStart) < time.Minute {
		hit = true
	}
	if rl.metrics != nil {
		if hit {
			rl.metrics.CacheHitsTotal.WithLabelValues("rate_limit").Inc()
		} else {
			rl.metrics.CacheMissesTotal.WithLabelValues("rate_limit").Inc()
		}
	}
	if !ok || now.Sub(w.windowStart) >= time.Duration(limit.Window)*time.Second {
		w = &window{count: 0, windowStart: now}
		classWindows[ip] = w
	}

	gw, ok := rl.global[ip]
	if !ok || now.Sub(gw.windowStart) >= time.Minute {
		gw = &window{count: 0, windowStart: now}
		rl.global[ip] = gw
	}

	if w.count >= limit.Requests {
		rl.updateCacheSizeLocked()
		return false, remainingSeconds(w.windowStart, time.Duration(limit.Window)*time.Second, now)
	}
	if gw.count >= rl.cfg.GlobalBurst {
		rl.updateCacheSizeLocked()
		return false, remainingSeconds(gw.windowStart, time.Minute, now)
	}

	w.count++
	gw.count++
	rl.updateCacheSizeLocked()
	return true, 0
}

func (rl *RateLimiter) updateCacheSizeLocked() {
	if rl.metrics == nil {
		return
	}
	items := 0
	var bytes int64
	for _, windows := range rl.byClass {
		for ip := range windows {
			items++
			bytes += int64(len(ip) + 32)
		}
	}
	for ip := range rl.global {
		items++
		bytes += int64(len(ip) + 32)
	}
	rl.metrics.SetCacheSize("rate_limit", items, bytes)
}

// sweepLocked drops every per-IP window whose period has already elapsed,
// at most once per rlSweepInterval. An expired window carries no state —
// Allow resets it in place on the next request from that IP — so removing
// it is behaviourally identical to keeping it, but bounds memory to the
// set of IPs active within the last window. rl.mu must be held.
func (rl *RateLimiter) sweepLocked(now time.Time) {
	if now.Sub(rl.lastSweep) < rlSweepInterval {
		return
	}
	rl.lastSweep = now

	for class, windows := range rl.byClass {
		span := time.Duration(rl.classLimit(class).Window) * time.Second
		for ip, w := range windows {
			if now.Sub(w.windowStart) >= span {
				delete(windows, ip)
			}
		}
	}
	for ip, gw := range rl.global {
		if now.Sub(gw.windowStart) >= time.Minute {
			delete(rl.global, ip)
		}
	}
}

// remainingSeconds returns the whole seconds left until start+span,
// floored at 1 so a Retry-After header is never "0".
func remainingSeconds(start time.Time, span time.Duration, now time.Time) int {
	remaining := int((span - now.Sub(start)).Seconds())
	if remaining < 1 {
		remaining = 1
	}
	return remaining
}

// classify returns the rate-limit class for a request, per AI.md PART 12
// "Rate Limiting" table: health endpoints get their own class, GET/HEAD are
// reads, everything else is a write.
func classify(method, path string) rlClass {
	if isHealthPath(path) {
		return rlHealth
	}
	if method == "GET" || method == "HEAD" {
		return rlRead
	}
	return rlWrite
}

// isHealthPath reports whether path is a health/status endpoint, per
// AI.md PART 13.
func isHealthPath(path string) bool {
	switch path {
	case "/server/healthz", "/healthz", "/api/healthz":
		return true
	}
	return len(path) > len("/server/healthz") && path[len(path)-len("/server/healthz"):] == "/server/healthz"
}
