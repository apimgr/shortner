// Package httpserver implements the minimal chi-based HTTP server skeleton
// that hosts the PART 12 middleware chain and the PART 13 health/versioning
// endpoints. See AI.md PART 12 "Server Configuration" and PART 13 "Health &
// Versioning". Full route surfaces (link creation, redirects, the frontend
// template engine) are out of scope here — see PART 14/16, tracked in
// TODO.AI.md.
package httpserver

import "context"

// ctxKey is an unexported type for context values set by this package's
// middleware, avoiding collisions with keys set elsewhere.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyAllowlisted
	ctxKeyOperator
)

// RequestIDFromContext returns the request ID attached by RequestIDMiddleware,
// or "" if none is present.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// IsAllowlisted reports whether AllowlistMiddleware marked this request's
// client IP as allowlisted. Always false until PART 11's allowlist config
// is implemented (see TODO.AI.md).
func IsAllowlisted(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyAllowlisted).(bool)
	return v
}

// IsOperator reports whether AuthMiddleware validated an operator token
// (server.token) on this request.
func IsOperator(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyOperator).(bool)
	return v
}
