package cache

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisOptions configures the Valkey/Redis cache driver.
type RedisOptions struct {
	URL           string
	Host          string
	Port          int
	Username      string
	Password      string
	DB            int
	TLS           bool
	TLSSkipVerify bool
	TLSCA         string
	TLSCert       string
	TLSKey        string
	PoolSize      int
	MinIdle       int
	Timeout       time.Duration
	Prefix        string
	ConfigDir     string
}

// Redis is a Valkey/Redis-backed implementation of Cache.
type Redis struct {
	client *redis.Client
	prefix string
}

// AllowRate atomically increments a per-class and global fixed-window pair.
// The returned boolean is false when Redis could not decide and the caller
// should use its local limiter instead.
func (r *Redis) AllowRate(classKey, ip string, classLimit, globalLimit int, classWindow, globalWindow time.Duration) (allowed bool, retryAfter int, decided bool) {
	if r == nil || r.client == nil || classLimit <= 0 || globalLimit <= 0 {
		return false, 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	classKey = r.prefix + "rate:" + classKey
	globalKey := r.prefix + "rate:global:" + ip
	result, err := rateLimitScript.Run(ctx, r.client, []string{classKey, globalKey}, classLimit, globalLimit, classWindow.Milliseconds(), globalWindow.Milliseconds()).Slice()
	if err != nil || len(result) != 2 {
		return false, 0, false
	}
	allow, _ := redisInt64(result[0])
	retryMillis, _ := redisInt64(result[1])
	return allow == 1, int((retryMillis + 999) / 1000), true
}

// Check verifies the remote cache is still reachable for health reporting.
func (r *Redis) Check(ctx context.Context) error {
	if r == nil || r.client == nil {
		return errors.New("cache: client unavailable")
	}
	return r.client.Ping(ctx).Err()
}

var rateLimitScript = redis.NewScript(`
local classCount = tonumber(redis.call('GET', KEYS[1]) or '0')
local globalCount = tonumber(redis.call('GET', KEYS[2]) or '0')
local classLimit = tonumber(ARGV[1])
local globalLimit = tonumber(ARGV[2])
if classCount >= classLimit or globalCount >= globalLimit then
  local ttl = 0
  if classCount >= classLimit then ttl = redis.call('PTTL', KEYS[1]) end
  if globalCount >= globalLimit then ttl = math.max(ttl, redis.call('PTTL', KEYS[2])) end
  ttl = math.max(ttl, 0)
  return {0, ttl}
end
local classNext = redis.call('INCR', KEYS[1])
if classNext == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[3]) end
local globalNext = redis.call('INCR', KEYS[2])
if globalNext == 1 then redis.call('PEXPIRE', KEYS[2], ARGV[4]) end
return {1, 0}
`)

func redisInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case int64:
		return number, true
	case string:
		parsed, err := strconv.ParseInt(number, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

// NewRedis creates and verifies a remote cache connection.
func NewRedis(ctx context.Context, cfg RedisOptions) (*Redis, error) {
	opts, err := redisOptions(cfg)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, errors.New("cache: connection failed")
	}
	return &Redis{client: client, prefix: cfg.Prefix}, nil
}

func redisOptions(cfg RedisOptions) (*redis.Options, error) {
	var opts *redis.Options
	if strings.TrimSpace(cfg.URL) != "" {
		u, err := url.Parse(strings.TrimSpace(cfg.URL))
		if err != nil || u.Host == "" || u.User == nil && strings.Contains(u.String(), "@") {
			return nil, errors.New("cache: invalid connection URL")
		}
		scheme := strings.ToLower(u.Scheme)
		switch scheme {
		case "valkey":
			u.Scheme = "redis"
		case "valkeys":
			u.Scheme = "rediss"
		case "redis", "rediss":
		default:
			return nil, errors.New("cache: unsupported connection URL scheme")
		}
		query := u.Query()
		var queryPassword string
		for key, values := range query {
			if len(values) == 0 {
				continue
			}
			switch strings.ToLower(key) {
			case "password", "pwd", "token", "authtoken":
				queryPassword = values[0]
				query.Del(key)
			}
		}
		if value := query.Get("tls"); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, errors.New("cache: invalid TLS option")
			}
			cfg.TLS = parsed
			query.Del("tls")
		}
		if value := query.Get("tls_skip_verify"); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, errors.New("cache: invalid TLS verification option")
			}
			cfg.TLSSkipVerify = parsed
			query.Del("tls_skip_verify")
		}
		if value := query.Get("sslmode"); value != "" {
			switch strings.ToLower(value) {
			case "disable":
				cfg.TLS = false
			case "require":
				cfg.TLS = true
				cfg.TLSSkipVerify = true
			case "verify-full":
				cfg.TLS = true
				cfg.TLSSkipVerify = false
			default:
				return nil, errors.New("cache: invalid TLS mode")
			}
			query.Del("sslmode")
		}
		if value := query.Get("encrypt"); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, errors.New("cache: invalid TLS option")
			}
			cfg.TLS = parsed
			query.Del("encrypt")
		}
		u.RawQuery = query.Encode()
		if u.User != nil {
			username := u.User.Username()
			password, hasPassword := u.User.Password()
			if !hasPassword {
				password, username = username, ""
				u.User = url.UserPassword(username, password)
			}
			cfg.Username, cfg.Password = username, password
		}
		if queryPassword != "" {
			cfg.Password = queryPassword
			if cfg.Username == "" {
				u.User = url.UserPassword("", queryPassword)
			} else {
				u.User = url.UserPassword(cfg.Username, queryPassword)
			}
		}
		if cfg.Password != "" {
			if cfg.Username == "" {
				u.User = url.UserPassword("", cfg.Password)
			} else {
				u.User = url.UserPassword(cfg.Username, cfg.Password)
			}
		}
		parsed, err := redis.ParseURL(u.String())
		if err != nil {
			return nil, errors.New("cache: invalid connection URL")
		}
		opts = parsed
		if scheme == "rediss" || scheme == "valkeys" {
			cfg.TLS = true
		}
	} else {
		if cfg.Host == "" {
			cfg.Host = "localhost"
		}
		if cfg.Port <= 0 || cfg.Port > 65535 {
			cfg.Port = 6379
		}
		opts = &redis.Options{
			Addr: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port), Username: cfg.Username,
			Password: cfg.Password, DB: cfg.DB,
		}
	}
	if cfg.PoolSize > 0 {
		opts.PoolSize = cfg.PoolSize
	}
	if cfg.MinIdle > 0 {
		opts.MinIdleConns = cfg.MinIdle
	}
	if cfg.Timeout > 0 {
		opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = cfg.Timeout, cfg.Timeout, cfg.Timeout
	}
	if cfg.TLS {
		tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: cfg.TLSSkipVerify}
		if cfg.TLSCA != "" {
			caPath := resolveTLSPath(cfg.ConfigDir, cfg.TLSCA)
			pem, err := os.ReadFile(caPath)
			if err != nil {
				return nil, errors.New("cache: cannot read TLS CA file")
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, errors.New("cache: invalid TLS CA file")
			}
			tlsConfig.RootCAs = pool
			tlsConfig.InsecureSkipVerify = false
		}
		if cfg.TLSCert != "" || cfg.TLSKey != "" {
			if cfg.TLSCert == "" || cfg.TLSKey == "" {
				return nil, errors.New("cache: both TLS client certificate and key are required")
			}
			cert, err := tls.LoadX509KeyPair(resolveTLSPath(cfg.ConfigDir, cfg.TLSCert), resolveTLSPath(cfg.ConfigDir, cfg.TLSKey))
			if err != nil {
				return nil, errors.New("cache: invalid TLS client certificate")
			}
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
		opts.TLSConfig = tlsConfig
	} else if strings.HasPrefix(strings.ToLower(cfg.URL), "rediss://") || strings.HasPrefix(strings.ToLower(cfg.URL), "valkeys://") {
		return nil, errors.New("cache: secure URL cannot disable TLS")
	}
	return opts, nil
}

func resolveTLSPath(configDir, value string) string {
	if filepath.IsAbs(value) || configDir == "" {
		return value
	}
	return filepath.Join(configDir, "ssl", value)
}

// Get implements Cache. The remote cache stores JSON so values are portable
// across processes and do not depend on Go's in-memory concrete types.
func (r *Redis) Get(ctx context.Context, key string) (any, bool) {
	value, err := r.client.Get(ctx, r.prefix+key).Bytes()
	if err != nil {
		return nil, false
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return nil, false
	}
	return decoded, true
}

// Set implements Cache.
func (r *Redis) Set(ctx context.Context, key string, value any, ttl time.Duration) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = r.client.Set(ctx, r.prefix+key, encoded, ttl).Err()
}

// Delete implements Cache.
func (r *Redis) Delete(ctx context.Context, key string) {
	_ = r.client.Del(ctx, r.prefix+key).Err()
}

// GetOrSet implements Cache.
func (r *Redis) GetOrSet(ctx context.Context, key string, ttl time.Duration, fn func() (any, error)) (any, error) {
	if value, ok := r.Get(ctx, key); ok {
		return value, nil
	}
	value, err := fn()
	if err != nil {
		return nil, err
	}
	r.Set(ctx, key, value, ttl)
	return value, nil
}

// Close closes the underlying Redis client.
func (r *Redis) Close() error { return r.client.Close() }
