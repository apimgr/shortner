// Package cache implements the caching layer described in AI.md PART 9
// "Caching". It defines the driver-agnostic Cache interface plus the
// in-process "memory" driver (default, used in development and small
// deployments). The "valkey"/"redis" drivers described in the spec depend
// on the full server.yml cache config schema (AI.md PART 12, not yet
// implemented) and a client dependency — tracked in TODO.AI.md.
package cache

import (
	"context"
	"strings"
	"sync"
	"time"
)

// TTL defaults per AI.md PART 9 "Cache TTL Defaults".
const (
	TTLRateLimitCounter = 1 * time.Minute
	TTLConfig           = 1 * time.Minute
	TTLStaticContent    = 24 * time.Hour
	TTLGeoIP            = 7 * 24 * time.Hour
	TTLBlocklist        = 1 * time.Hour
	TTLPage             = 5 * time.Minute
	TTLAPIResponse      = 30 * time.Second
	// TTLNoExpiry marks entries that never expire (e.g. API tokens),
	// per AI.md PART 9 "Cache TTL Defaults".
	TTLNoExpiry = time.Duration(0)
)

// Cache is the driver-agnostic caching interface used throughout the
// application. All implementations must be safe for concurrent use.
type Cache interface {
	// Get returns the cached value for key and true, or nil/false if the
	// key is absent or expired.
	Get(ctx context.Context, key string) (any, bool)
	// Set stores value under key. ttl == 0 means "never expires".
	Set(ctx context.Context, key string, value any, ttl time.Duration)
	// Delete removes key, if present.
	Delete(ctx context.Context, key string)
	// GetOrSet returns the cached value for key, or calls fn, caches the
	// result under ttl, and returns it.
	GetOrSet(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error)
}

// Key builds a hierarchical, colon-separated cache key from parts, per
// AI.md PART 9 "Cache Key Naming" ("Use colons as separators, lowercase
// only"). Each part is lowercased individually.
func Key(parts ...string) string {
	lowered := make([]string, len(parts))
	for i, p := range parts {
		lowered[i] = strings.ToLower(p)
	}
	return strings.Join(lowered, ":")
}

// entry is one memory-cache record.
type entry struct {
	value     any
	expiresAt time.Time
	noExpiry  bool
}

func (e entry) expired(now time.Time) bool {
	return !e.noExpiry && now.After(e.expiresAt)
}

// Memory is the default in-process cache driver, per AI.md PART 9 "Cache
// Drivers". Entries are lost on restart.
type Memory struct {
	mu      sync.RWMutex
	entries map[string]entry
}

// NewMemory returns an empty Memory cache.
func NewMemory() *Memory {
	return &Memory{entries: make(map[string]entry)}
}

// Get implements Cache.
func (m *Memory) Get(_ context.Context, key string) (any, bool) {
	m.mu.RLock()
	e, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if e.expired(time.Now()) {
		m.Delete(context.Background(), key)
		return nil, false
	}
	return e.value, true
}

// Set implements Cache. ttl == 0 means the entry never expires.
func (m *Memory) Set(_ context.Context, key string, value any, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ttl <= 0 {
		m.entries[key] = entry{value: value, noExpiry: true}
		return
	}
	m.entries[key] = entry{value: value, expiresAt: time.Now().Add(ttl)}
}

// Delete implements Cache.
func (m *Memory) Delete(_ context.Context, key string) {
	m.mu.Lock()
	delete(m.entries, key)
	m.mu.Unlock()
}

// GetOrSet implements Cache.
func (m *Memory) GetOrSet(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	if v, ok := m.Get(ctx, key); ok {
		return v, nil
	}
	v, err := fn()
	if err != nil {
		return nil, err
	}
	m.Set(ctx, key, v, ttl)
	return v, nil
}

// Sweep removes every expired entry. Callers may run this periodically
// (e.g. from the scheduler, AI.md PART 18) to bound memory use; Get/Set
// are self-cleaning on access regardless.
func (m *Memory) Sweep() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, e := range m.entries {
		if e.expired(now) {
			delete(m.entries, k)
		}
	}
}

// Len returns the current number of stored entries, expired or not.
func (m *Memory) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.entries)
}
