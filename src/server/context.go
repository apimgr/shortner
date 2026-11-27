// Package server implements the chi-based server, middleware, APIs, and
// server-rendered frontend described by AI.md PARTS 12–16.
package server

import (
	"context"
	"net/http"
)

// ctxKey is an unexported type for context values set by this package's
// middleware, avoiding collisions with keys set elsewhere.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyAllowlisted
	ctxKeyOperator
	ctxKeyGPCOptOut
	ctxKeyLang
	ctxKeyOriginalPath
)

func originalRequestPath(r *http.Request) string {
	if p, ok := r.Context().Value(ctxKeyOriginalPath).(string); ok {
		return p
	}
	return r.URL.Path
}

// RequestIDFromContext returns the request ID attached by RequestIDMiddleware,
// or "" if none is present.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// IsAllowlisted reports whether AllowlistMiddleware marked this request's
// client IP as allowlisted, per AI.md PART 11 "IP Block Management" ->
// "Allowlist (Trusted IPs)". Downstream stages (blocklist, rate limit,
// GeoIP, auto-block) skip enforcement when this is true; auth, CSRF, and
// path security ignore it entirely.
func IsAllowlisted(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyAllowlisted).(bool)
	return v
}

// IsGPCOptOut reports whether the request carried `Sec-GPC: 1` and the
// operator honors it (`web.headers.honor_sec_gpc`, default true), per
// AI.md PART 11 "Privacy Signal Headers". When true the request must skip
// personalization, behavioral analytics, and every non-essential cookie.
func IsGPCOptOut(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyGPCOptOut).(bool)
	return v
}

// IsOperator reports whether AuthMiddleware validated an operator token
// (server.token) on this request.
func IsOperator(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyOperator).(bool)
	return v
}
