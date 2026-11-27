package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apimgr/shortner/src/db"
)

func TestAuthCheckRequiresBearerAndReturnsTokenKind(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bearer     string
		operator   bool
		ownerToken *db.APIToken
		status     int
		kind       string
	}{
		{name: "missing bearer", ownerToken: &db.APIToken{}, status: http.StatusUnauthorized},
		{name: "invalid bearer", bearer: "invalid", status: http.StatusUnauthorized},
		{name: "operator", bearer: "operator-token", operator: true, status: http.StatusOK, kind: "operator"},
		{name: "owner", bearer: "owner-token", ownerToken: &db.APIToken{Kind: "primary"}, status: http.StatusOK, kind: "owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/server/auth/check", nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			ctx := context.WithValue(req.Context(), ctxKeyOperator, tc.operator)
			if tc.ownerToken != nil {
				ctx = context.WithValue(ctx, authTokenContextKey{}, tc.ownerToken)
			}
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			authCheckHandler(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.kind == "" {
				return
			}
			var body struct {
				OK   bool              `json:"ok"`
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if !body.OK || body.Data["kind"] != tc.kind {
				t.Fatalf("response = %+v, want kind %q", body, tc.kind)
			}
		})
	}
}
