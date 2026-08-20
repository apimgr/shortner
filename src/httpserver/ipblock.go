// Allowlist, IP blocking, and abuse detection, per AI.md PART 11
// "Abuse Detection" and "IP Block Management".
package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/notify"
)

// BlockType distinguishes an auto-releasing block from an operator's
// permanent config-file entry, per AI.md PART 11 "Block Types".
type BlockType string

// Block types, per AI.md PART 11 "IP Block Management" -> "Block Types".
const (
	BlockTypeTemporary BlockType = "temporary"
	BlockTypePermanent BlockType = "permanent"
)

// IPBlock is one blocked address or range, per AI.md PART 11 "IP Block
// Data Model".
type IPBlock struct {
	IP           string     `json:"ip"`
	CIDR         string     `json:"cidr,omitempty"`
	Type         BlockType  `json:"type"`
	Reason       string     `json:"reason"`
	BlockedAt    time.Time  `json:"blocked_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	OffenseCount int        `json:"offense_count"`
	AutoBlocked  bool       `json:"auto_blocked"`
}

// AllowlistLookup answers "is this IP trusted?" from the parsed
// `server.security.allowlist` config. Entries are normalized to CIDR form
// by config.Validate before they reach here.
type AllowlistLookup struct {
	networks []*net.IPNet
}

// NewAllowlistLookup parses the configured allowlist. Entries that fail
// to parse were already dropped with a warning by config.Validate; any
// that still fail here are skipped rather than failing startup, per the
// Config Validation Rule.
func NewAllowlistLookup(entries []config.AllowlistEntry) *AllowlistLookup {
	l := &AllowlistLookup{}
	for _, e := range entries {
		if _, network, err := net.ParseCIDR(e.CIDR); err == nil {
			l.networks = append(l.networks, network)
		}
	}
	return l
}

// Contains reports whether ip falls inside any allowlisted range.
func (l *AllowlistLookup) Contains(ip string) bool {
	if l == nil || len(l.networks) == 0 {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, network := range l.networks {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// BlockStore holds the permanent blocks from config plus the temporary
// blocks abuse detection adds at runtime. Temporary blocks live in memory
// only: they auto-release on expiry, and a restart is itself a release.
// Permanent blocks are config-file entries, per AI.md PART 11 "Block
// Types" -> "Config file entry only".
type BlockStore struct {
	mu        sync.RWMutex
	permanent []*net.IPNet
	reasons   map[string]string
	temporary map[string]IPBlock
	audit     *applog.AuditLogger
}

// NewBlockStore builds the store from the configured permanent entries.
func NewBlockStore(entries []config.BlockedIPEntry, audit *applog.AuditLogger) *BlockStore {
	s := &BlockStore{
		reasons:   map[string]string{},
		temporary: map[string]IPBlock{},
		audit:     audit,
	}
	for _, e := range entries {
		network, err := parseNetwork(e.CIDR)
		if err != nil {
			continue
		}
		s.permanent = append(s.permanent, network)
		s.reasons[network.String()] = e.Reason
	}
	return s
}

// parseNetwork parses a normalized CIDR entry.
func parseNetwork(cidr string) (*net.IPNet, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	return network, nil
}

// Blocked reports whether ip is currently blocked, and by which entry.
// Expired temporary blocks are treated as released even before the
// scheduler sweep runs, so an expiry can never outlive its window.
func (s *BlockStore) Blocked(ip string, now time.Time) (IPBlock, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if block, ok := s.temporary[ip]; ok {
		if block.ExpiresAt == nil || block.ExpiresAt.After(now) {
			return block, true
		}
	}

	parsed := net.ParseIP(ip)
	if parsed == nil {
		return IPBlock{}, false
	}
	for _, network := range s.permanent {
		if network.Contains(parsed) {
			return IPBlock{
				IP:        ip,
				CIDR:      network.String(),
				Type:      BlockTypePermanent,
				Reason:    s.reasons[network.String()],
				BlockedAt: now,
			}, true
		}
	}
	return IPBlock{}, false
}

// BlockTemporarily adds (or extends) a temporary block and emits the
// `security.ip_blocked` audit event, per AI.md PART 11 "Audit Events".
func (s *BlockStore) BlockTemporarily(ip, reason string, duration time.Duration, now time.Time) {
	expires := now.Add(duration)

	s.mu.Lock()
	previous := s.temporary[ip]
	block := IPBlock{
		IP:           ip,
		Type:         BlockTypeTemporary,
		Reason:       reason,
		BlockedAt:    now,
		ExpiresAt:    &expires,
		OffenseCount: previous.OffenseCount + 1,
		AutoBlocked:  true,
	}
	s.temporary[ip] = block
	s.mu.Unlock()

	s.writeAudit("security.ip_blocked", ip, map[string]any{
		"reason":        reason,
		"duration":      duration.String(),
		"offense_count": block.OffenseCount,
	})
}

// Unblock removes a temporary block and emits `security.ip_unblocked`.
// Permanent blocks are config-file state and are never removed here.
func (s *BlockStore) Unblock(ip, reason string) bool {
	s.mu.Lock()
	_, ok := s.temporary[ip]
	if ok {
		delete(s.temporary, ip)
	}
	s.mu.Unlock()

	if ok {
		s.writeAudit("security.ip_unblocked", ip, map[string]any{"reason": reason})
	}
	return ok
}

// ReleaseExpired drops every temporary block whose window has passed and
// returns how many were released. The scheduler calls this every minute,
// per AI.md PART 11 "Auto-Release".
func (s *BlockStore) ReleaseExpired(now time.Time) int {
	s.mu.Lock()
	var expired []string
	for ip, block := range s.temporary {
		if block.ExpiresAt != nil && !block.ExpiresAt.After(now) {
			expired = append(expired, ip)
		}
	}
	for _, ip := range expired {
		delete(s.temporary, ip)
	}
	s.mu.Unlock()

	for _, ip := range expired {
		s.writeAudit("security.ip_unblocked", ip, map[string]any{"reason": "block duration expired"})
	}
	return len(expired)
}

// ReleaseAllowlisted drops temporary blocks for any IP that is now
// allowlisted — the second auto-release trigger in AI.md PART 11
// "Auto-Release".
func (s *BlockStore) ReleaseAllowlisted(allow *AllowlistLookup) int {
	if allow == nil {
		return 0
	}
	s.mu.Lock()
	var released []string
	for ip := range s.temporary {
		if allow.Contains(ip) {
			released = append(released, ip)
		}
	}
	for _, ip := range released {
		delete(s.temporary, ip)
	}
	s.mu.Unlock()

	for _, ip := range released {
		s.writeAudit("security.ip_unblocked", ip, map[string]any{"reason": "ip added to allowlist"})
	}
	return len(released)
}

// writeAudit appends one security audit entry, ignoring a nil logger so
// the store works in tests and in CLI contexts with no audit file open.
func (s *BlockStore) writeAudit(event, ip string, details map[string]any) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Write(applog.Entry{
		Time:     time.Now().UTC(),
		Event:    event,
		Category: "security",
		Severity: applog.SeverityWarn,
		Actor:    applog.Actor{IP: ip},
		Target:   &applog.Target{Type: "ip", ID: ip},
		Details:  details,
		Result:   applog.ResultSuccess,
	})
}

// AbuseDetector counts requests per IP in a rolling window and triggers a
// temporary block once an IP exceeds the flood threshold, per AI.md
// PART 11 "Abuse Detection" -> "Request Flood": 10x the rate limit in a
// short burst from the same IP.
type AbuseDetector struct {
	cfg       config.AbuseDetection
	threshold int
	duration  time.Duration
	window    time.Duration

	mu      sync.Mutex
	counts  map[string]int
	resetAt time.Time
}

// NewAbuseDetector builds a detector whose flood threshold is the
// configured multiplier applied to the per-minute rate limit. It returns
// nil when abuse detection is disabled, which every caller tolerates.
func NewAbuseDetector(cfg config.AbuseDetection, perMinuteLimit int) *AbuseDetector {
	if !cfg.Enabled || perMinuteLimit <= 0 {
		return nil
	}
	duration, err := config.ParseDuration(cfg.RequestFlood.BlockDuration, time.Hour)
	if err != nil {
		duration = time.Hour
	}
	window := time.Minute
	return &AbuseDetector{
		cfg:       cfg,
		threshold: perMinuteLimit * cfg.RequestFlood.Multiplier,
		duration:  duration,
		window:    window,
		counts:    map[string]int{},
		resetAt:   time.Now().Add(window),
	}
}

// Record counts one request from ip and reports whether it just crossed
// the flood threshold. The counter resets each window so a steady, legal
// request rate never accumulates into a block.
func (a *AbuseDetector) Record(ip string, now time.Time) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if now.After(a.resetAt) {
		a.counts = map[string]int{}
		a.resetAt = now.Add(a.window)
	}
	a.counts[ip]++
	if a.counts[ip] != a.threshold {
		return false
	}
	return a.cfg.AutoBlockIP
}

// BlockDuration is how long a flood block lasts.
func (a *AbuseDetector) BlockDuration() time.Duration {
	if a == nil {
		return 0
	}
	return a.duration
}

// allowlistMiddleware sets the allowlisted context flag (execution
// position 5), per AI.md PART 11 "IP Block Management" -> "Middleware".
// Downstream stages read it via IsAllowlisted; the auth middleware
// deliberately ignores it.
func (d *deps) allowlistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowlisted := d.allowlist.Contains(d.resolver.ResolveClientIP(r))
		ctx := context.WithValue(r.Context(), ctxKeyAllowlisted, allowlisted)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// blocklistMiddleware rejects requests from blocked IPs and runs flood
// detection (execution position 6), per AI.md PART 11 "IP Block
// Management" and "Abuse Detection". Allowlisted IPs bypass both — they
// are never blocked and never auto-blocked.
func (d *deps) blocklistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsAllowlisted(r.Context()) || d.blocks == nil {
			next.ServeHTTP(w, r)
			return
		}

		ip := d.resolver.ResolveClientIP(r)
		now := time.Now()
		if _, blocked := d.blocks.Blocked(ip, now); blocked {
			// The response deliberately does not say the IP is blocked or
			// for how long — that is Tier 1 detail under the Public
			// Endpoint Safety Principle (AI.md PART 11).
			sendError(w, r, apperr.New(apperr.CodeForbidden))
			return
		}

		if d.abuse.Record(ip, now) {
			d.blocks.BlockTemporarily(ip, "request flood", d.abuse.BlockDuration(), now)
			d.notifyAbuseBlock(ip, now)
			sendError(w, r, apperr.New(apperr.CodeForbidden))
			return
		}

		next.ServeHTTP(w, r)
	})
}

// notifyAbuseBlock raises the AI.md PART 17 `security_alert` event for an
// abuse-detection block. The blocked IP is an attacker address in an
// operator-only email, not visitor PII, so PART 17's own `{ip}` variable
// carries it verbatim.
func (d *deps) notifyAbuseBlock(ip string, now time.Time) {
	_ = d.notifier.Send(notify.EventSecurityAlert, map[string]string{
		"event": "IP blocked for request flooding",
		"ip":    ip,
		"details": fmt.Sprintf("%s exceeded the flood threshold and is blocked until %s.",
			ip, now.Add(d.abuse.BlockDuration()).UTC().Format(time.RFC3339)),
	})
}
