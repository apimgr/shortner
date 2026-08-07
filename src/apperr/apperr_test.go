package apperr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestMapCodeToHTTPStatus(t *testing.T) {
	cases := map[Code]int{
		CodeBadRequest:       400,
		CodeValidationFailed: 400,
		CodeUnauthorized:     401,
		CodeTokenExpired:     401,
		CodeTokenInvalid:     401,
		CodeForbidden:        403,
		CodeAccountLocked:    403,
		CodeCSRFFailed:       403,
		CodeNotFound:         404,
		CodeGone:             410,
		CodeMethodNotAllowed: 405,
		CodeConflict:         409,
		CodeRateLimited:      429,
		CodeServerError:      500,
		CodeMaintenance:      503,
		Code("UNKNOWN"):      500,
	}
	for code, want := range cases {
		if got := MapCodeToHTTPStatus(code); got != want {
			t.Errorf("MapCodeToHTTPStatus(%s) = %d, want %d", code, got, want)
		}
	}
}

func TestNewUsesDefaultMessage(t *testing.T) {
	err := New(CodeNotFound)
	if err.Message != "Resource not found" {
		t.Errorf("Message = %q, want %q", err.Message, "Resource not found")
	}
	if err.HTTPStatus != 404 {
		t.Errorf("HTTPStatus = %d, want 404", err.HTTPStatus)
	}
}

func TestWrapCarriesInternal(t *testing.T) {
	internal := errors.New("boom")
	err := Wrap(CodeServerError, internal)
	if !errors.Is(err, internal) {
		t.Errorf("errors.Is(err, internal) = false, want true")
	}
	if err.Error() == "" {
		t.Errorf("Error() empty")
	}
}

func TestWithMessageAndDetails(t *testing.T) {
	base := New(CodeValidationFailed)
	got := base.WithMessage("bad field").WithDetails(map[string]any{"field": "email"})
	if base.Message == "bad field" {
		t.Errorf("WithMessage mutated original")
	}
	if got.Message != "bad field" {
		t.Errorf("Message = %q, want %q", got.Message, "bad field")
	}
	if got.Details["field"] != "email" {
		t.Errorf("Details[field] = %v, want email", got.Details["field"])
	}
}

func TestSendOK(t *testing.T) {
	w := httptest.NewRecorder()
	SendOK(w, map[string]string{"foo": "bar"})

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !resp.OK {
		t.Errorf("OK = false, want true")
	}
}

func TestSendError(t *testing.T) {
	w := httptest.NewRecorder()
	SendError(w, New(CodeNotFound))

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var resp APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.OK {
		t.Errorf("OK = true, want false")
	}
	if resp.Error != "NOT_FOUND" {
		t.Errorf("Error = %q, want NOT_FOUND", resp.Error)
	}
}

func TestIsRetryable(t *testing.T) {
	if IsRetryable(nil) {
		t.Errorf("IsRetryable(nil) = true, want false")
	}
	if !IsRetryable(context.DeadlineExceeded) {
		t.Errorf("IsRetryable(DeadlineExceeded) = false, want true")
	}
	if IsRetryable(New(CodeBadRequest)) {
		t.Errorf("IsRetryable(4xx) = true, want false")
	}
	if !IsRetryable(New(CodeServerError)) {
		t.Errorf("IsRetryable(5xx) = false, want true")
	}
}

func TestWithRetrySucceedsAfterTransientFailure(t *testing.T) {
	attempts := 0
	err := WithRetry(context.Background(), func() error {
		attempts++
		if attempts < 2 {
			return New(CodeServerError)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithRetry() error = %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestWithRetryStopsOnNonRetryable(t *testing.T) {
	attempts := 0
	err := WithRetry(context.Background(), func() error {
		attempts++
		return New(CodeBadRequest)
	})
	if err == nil {
		t.Fatalf("WithRetry() error = nil, want error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

func TestLogDoesNotPanicOnNilLogger(t *testing.T) {
	Log(context.Background(), nil, New(CodeServerError))
}
