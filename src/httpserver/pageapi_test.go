package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityPolicyAPIHandlerUsesDefaultAndConfiguredPolicy(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	for _, tc := range []struct {
		name   string
		policy string
		want   string
	}{
		{name: "default", want: defaultDisclosurePolicy},
		{name: "configured", policy: "Operator policy", want: "Operator policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fd.cfg.Web.Security.Policy = tc.policy
			rec := httptest.NewRecorder()
			fd.securityPolicyAPIHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/server/security/policy", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var response struct {
				OK   bool `json:"ok"`
				Data struct {
					Policy string `json:"policy"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if !response.OK || response.Data.Policy != tc.want {
				t.Fatalf("response = %#v, want policy %q", response, tc.want)
			}
		})
	}
}

func TestDPOAPIHandlerAlwaysReturnsFallbackShape(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	fd.cfg.Server.Contact.DPO.Name = "Privacy Officer"
	fd.cfg.Server.Contact.General.Email = "privacy@example.test"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/server/dpo", nil)
	req.Host = "example.test"
	rec := httptest.NewRecorder()
	fd.dpoAPIHandler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			Name       string `json:"name"`
			Email      string `json:"email"`
			PrivacyURL string `json:"privacy_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Data.Name != "Privacy Officer" || response.Data.Email != "privacy@example.test" || response.Data.PrivacyURL == "" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestDPOAPIHandlerEmptyValuesRemainPresent(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	rec := httptest.NewRecorder()
	fd.dpoAPIHandler(rec, httptest.NewRequest(http.MethodGet, "/api/v1/server/dpo", nil))
	var response struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name", "email", "privacy_url"} {
		if _, ok := response.Data[key]; !ok {
			t.Errorf("response missing required key %q", key)
		}
	}
	if response.Data["email"] != "" {
		t.Errorf("email = %v, want empty string", response.Data["email"])
	}
}
