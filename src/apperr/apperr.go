// Package apperr implements the canonical API error/response envelope and
// error-code-to-HTTP-status mapping. See AI.md PART 9 "Error Handling" and
// PART 14 "API Structure" (the authoritative shape reference for the JSON
// body — this package must never redefine the shape, only implement it).
package apperr

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// APIResponse is the unified success/error envelope for every API response.
type APIResponse struct {
	OK      bool   `json:"ok"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

// Code is a stable machine-readable error code, per AI.md PART 9
// "Error Codes".
type Code string

// Standard error codes and their canonical messages, per AI.md PART 9.
const (
	CodeBadRequest       Code = "BAD_REQUEST"
	CodeValidationFailed Code = "VALIDATION_FAILED"
	CodeUnauthorized     Code = "UNAUTHORIZED"
	CodeTokenExpired     Code = "TOKEN_EXPIRED"
	CodeTokenInvalid     Code = "TOKEN_INVALID"
	CodeForbidden        Code = "FORBIDDEN"
	CodeAccountLocked    Code = "ACCOUNT_LOCKED"
	CodeCSRFFailed       Code = "CSRF_FAILED"
	CodeNotFound         Code = "NOT_FOUND"
	CodeMethodNotAllowed Code = "METHOD_NOT_ALLOWED"
	CodeConflict         Code = "CONFLICT"
	CodeRateLimited      Code = "RATE_LIMITED"
	CodeServerError      Code = "SERVER_ERROR"
	CodeMaintenance      Code = "MAINTENANCE"
	CodeGone             Code = "GONE"
)

// defaultMessages holds the canonical human-readable message per code, per
// AI.md PART 9 "Error Codes" table.
var defaultMessages = map[Code]string{
	CodeBadRequest:       "Invalid request format",
	CodeValidationFailed: "Validation failed",
	CodeUnauthorized:     "Authentication required",
	CodeTokenExpired:     "Token has expired",
	CodeTokenInvalid:     "Invalid token",
	CodeForbidden:        "Permission denied",
	CodeAccountLocked:    "Account locked",
	CodeCSRFFailed:       "CSRF token validation failed",
	CodeNotFound:         "Resource not found",
	CodeMethodNotAllowed: "Method not allowed",
	CodeConflict:         "Resource already exists",
	CodeRateLimited:      "Too many requests",
	CodeServerError:      "Internal server error",
	CodeMaintenance:      "Service unavailable",
	CodeGone:             "Resource is no longer available",
}

// DefaultMessage returns the canonical human-readable message for code, or
// a generic fallback if the code is unrecognized.
func DefaultMessage(code Code) string {
	if msg, ok := defaultMessages[code]; ok {
		return msg
	}
	return "Internal server error"
}

// AppError is the internal error type carried through handler chains. It
// separates the client-facing message from internal debugging context.
type AppError struct {
	Code       Code
	Message    string
	Details    map[string]any
	HTTPStatus int
	RequestID  string
	// MessageKey is the AI.md PART 30 translation key for Message. It is
	// set only when a handler supplies a non-canonical message; an empty
	// value means the canonical "errors.{code}" key applies.
	MessageKey string
	// Internal is never sent to the client — logged only.
	Internal error
}

// Error implements the error interface.
func (e *AppError) Error() string {
	if e.Internal != nil {
		return string(e.Code) + ": " + e.Message + ": " + e.Internal.Error()
	}
	return string(e.Code) + ": " + e.Message
}

// Unwrap allows errors.Is / errors.As to see the internal cause.
func (e *AppError) Unwrap() error {
	return e.Internal
}

// New builds an AppError for code, using the canonical default message and
// HTTP status mapping.
func New(code Code) *AppError {
	return &AppError{
		Code:       code,
		Message:    DefaultMessage(code),
		HTTPStatus: MapCodeToHTTPStatus(code),
	}
}

// Wrap builds an AppError for code that also carries an internal error for
// logging (never exposed to the client).
func Wrap(code Code, internal error) *AppError {
	e := New(code)
	e.Internal = internal
	return e
}

// WithMessage returns a copy of e with Message overridden.
func (e *AppError) WithMessage(msg string) *AppError {
	clone := *e
	clone.Message = msg
	return &clone
}

// WithMessageKey returns a copy of e whose client-facing message is the
// AI.md PART 30 translation key key, with fallback as the English text
// used by any caller that has no request language (CLI paths, logs).
func (e *AppError) WithMessageKey(key, fallback string) *AppError {
	clone := *e
	clone.MessageKey = key
	clone.Message = fallback
	return &clone
}

// TranslationKey returns the translation key for e's client-facing
// message: the explicit MessageKey when one was set, otherwise the
// canonical "errors.{lowercased_code}" key from AI.md PART 30
// "API Response Translation".
func (e *AppError) TranslationKey() string {
	if e.MessageKey != "" {
		return e.MessageKey
	}
	return "errors." + strings.ToLower(string(e.Code))
}

// WithDetails returns a copy of e with Details set.
func (e *AppError) WithDetails(details map[string]any) *AppError {
	clone := *e
	clone.Details = details
	return &clone
}

// MapCodeToHTTPStatus maps an error code to its HTTP status, per AI.md
// PART 9 "Error Implementation".
func MapCodeToHTTPStatus(code Code) int {
	switch code {
	case CodeBadRequest, CodeValidationFailed:
		return http.StatusBadRequest
	case CodeUnauthorized, CodeTokenExpired, CodeTokenInvalid:
		return http.StatusUnauthorized
	case CodeForbidden, CodeAccountLocked, CodeCSRFFailed:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeGone:
		return http.StatusGone
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case CodeConflict:
		return http.StatusConflict
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeMaintenance:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// WriteJSON encodes v as the project's canonical JSON body: 2-space
// indentation and exactly one trailing newline, per AI.md PART 14
// "Response Formatting". Every JSON response goes through this helper.
func WriteJSON(w http.ResponseWriter, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// SendOK writes a success envelope with status 200.
func SendOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	WriteJSON(w, APIResponse{OK: true, Data: data})
}

// SendError writes an error envelope, status mapped from err.Code.
func SendError(w http.ResponseWriter, err *AppError) {
	status := err.HTTPStatus
	if status == 0 {
		status = MapCodeToHTTPStatus(err.Code)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	WriteJSON(w, APIResponse{
		OK:      false,
		Error:   string(err.Code),
		Message: err.Message,
	})
}

// Log writes err to logger with full internal context. Per AI.md PART 9
// "Error Logging": 5xx logs at Error level, 4xx logs at Warn level; the
// internal cause is included in the log record but never in the HTTP
// response.
func Log(ctx context.Context, logger *slog.Logger, err *AppError) {
	if logger == nil {
		return
	}
	attrs := []any{
		slog.String("error_code", string(err.Code)),
		slog.Int("http_status", err.HTTPStatus),
	}
	if err.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", err.RequestID))
	}
	if err.Internal != nil {
		attrs = append(attrs, slog.String("internal", err.Internal.Error()))
	}

	switch {
	case err.HTTPStatus >= 500:
		logger.ErrorContext(ctx, err.Message, attrs...)
	case err.HTTPStatus >= 400:
		logger.WarnContext(ctx, err.Message, attrs...)
	default:
		logger.InfoContext(ctx, err.Message, attrs...)
	}
}

// backoffSchedule is the fixed retry wait schedule from AI.md PART 9
// "Retry and Backoff".
var backoffSchedule = []time.Duration{
	0,
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
}

const maxBackoff = 30 * time.Second

// WithRetry runs fn with exponential backoff per AI.md PART 9 "Retry and
// Backoff". Retries only when IsRetryable(err) is true; 4xx-style errors
// return immediately without retrying.
func WithRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < len(backoffSchedule); attempt++ {
		if attempt > 0 {
			wait := backoffSchedule[attempt]
			if wait > maxBackoff {
				wait = maxBackoff
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}

		if err := fn(); err != nil {
			if !IsRetryable(err) {
				return err
			}
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// IsRetryable reports whether err represents a transient failure (network
// error, timeout, or 5xx-mapped AppError) that is safe to retry. 4xx
// AppErrors are never retryable.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ae *AppError
	if errors.As(err, &ae) {
		return ae.HTTPStatus == 0 || ae.HTTPStatus >= 500
	}
	return false
}
