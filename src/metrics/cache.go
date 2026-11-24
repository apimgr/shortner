package metrics

import (
	"context"
	"time"

	"github.com/apimgr/shortner/src/cache"
)

// RecordCacheLookup records a driver-reported lookup, including atomic Redis
// rate-limit accesses that do not pass through Cache.Get.
func (m *Metrics) RecordCacheLookup(name string, hit bool) {
	if m == nil {
		return
	}
	if hit {
		m.CacheHitsTotal.WithLabelValues(name).Inc()
	} else {
		m.CacheMissesTotal.WithLabelValues(name).Inc()
	}
}

type cacheStats interface {
	Stats(context.Context) (int, int64, error)
}

// StartCacheCollector publishes an authoritative cache size snapshot while
// the supplied backend supports Stats.
func (m *Metrics) StartCacheCollector(ctx context.Context, backend cache.Cache, name string) {
	if m == nil || backend == nil {
		return
	}
	stats, ok := backend.(cacheStats)
	if !ok {
		return
	}
	collect := func() {
		child, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		items, bytes, err := stats.Stats(child)
		if err == nil {
			m.SetCacheSize(name, items, bytes)
		}
	}
	go func() {
		collect()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collect()
			}
		}
	}()
}

// MetricsCache wraps an existing cache.Cache and records successful and
// unsuccessful reads using the supplied PART 20 cache label.
type MetricsCache struct {
	cache   cache.Cache
	metrics *Metrics
	name    string
}

// NewMetricsCache returns an instrumented cache preserving the wrapped
// cache's behavior. The label is the cache name or purpose, such as
// "items", "config", or "rate_limit".
func NewMetricsCache(m *Metrics, wrapped cache.Cache, name string) *MetricsCache {
	return &MetricsCache{cache: wrapped, metrics: m, name: name}
}

// Get implements cache.Cache.
func (m *MetricsCache) Get(ctx context.Context, key string) (any, bool) {
	value, found := m.cache.Get(ctx, key)
	if m.metrics != nil {
		if found {
			m.metrics.CacheHitsTotal.WithLabelValues(m.name).Inc()
		} else {
			m.metrics.CacheMissesTotal.WithLabelValues(m.name).Inc()
		}
	}
	return value, found
}

// Set implements cache.Cache.
func (m *MetricsCache) Set(ctx context.Context, key string, value any, ttl time.Duration) {
	m.cache.Set(ctx, key, value, ttl)
}

// Delete implements cache.Cache.
func (m *MetricsCache) Delete(ctx context.Context, key string) {
	m.cache.Delete(ctx, key)
}

// GetOrSet implements cache.Cache and records the lookup as a hit or miss.
func (m *MetricsCache) GetOrSet(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	if value, found := m.Get(ctx, key); found {
		return value, nil
	}
	return m.cache.GetOrSet(ctx, key, ttl, fn)
}

// RecordCacheEviction increments cache_evictions_total. Cache drivers that
// perform eviction should call this when an entry is removed by an
// eviction policy; ordinary Delete calls are not evictions.
func (m *Metrics) RecordCacheEviction(name string) {
	if m != nil {
		m.CacheEvictionsTotal.WithLabelValues(name).Inc()
	}
}

// SetCacheSize updates the item and byte gauges from a cache driver's
// authoritative size snapshot. The cache interface does not define a size
// or byte-count contract, so callers provide values only when their driver
// can measure them accurately.
func (m *Metrics) SetCacheSize(name string, items int, bytes int64) {
	if m == nil {
		return
	}
	m.CacheSize.WithLabelValues(name).Set(float64(items))
	m.CacheBytes.WithLabelValues(name).Set(float64(bytes))
}
