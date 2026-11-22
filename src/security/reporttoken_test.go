package security

import (
	"testing"
	"time"
)

func TestReportStatusTokenUsesStoredSHA256Hash(t *testing.T) {
	token, digest, err := NewReportStatusToken()
	if err != nil {
		t.Fatalf("NewReportStatusToken() error = %v", err)
	}
	if len(token) != 64 || len(digest) != 64 {
		t.Fatalf("token/hash lengths = %d/%d, want 64/64", len(token), len(digest))
	}
	if token == digest || !ValidReportStatusToken(digest, token) {
		t.Fatal("generated token was not validated by its stored digest")
	}
	if ValidReportStatusToken(digest, token+"x") || ValidReportStatusToken("invalid", token) {
		t.Fatal("invalid status token or digest was accepted")
	}
}

func TestReportToken(t *testing.T) {
	tests := []struct {
		name       string
		secret     string
		trackingID string
		wantEmpty  bool
	}{
		{name: "derives a token", secret: "install", trackingID: "sec_abcdef0123456789"},
		{name: "no secret", secret: "", trackingID: "sec_abcdef0123456789", wantEmpty: true},
		{name: "no tracking id", secret: "install", trackingID: "", wantEmpty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReportToken(tt.secret, tt.trackingID)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("ReportToken() = %q, want empty", got)
				}
				return
			}
			if len(got) != reportTokenLength {
				t.Fatalf("ReportToken() length = %d, want %d", len(got), reportTokenLength)
			}
			if got != ReportToken(tt.secret, tt.trackingID) {
				t.Error("ReportToken() is not deterministic")
			}
		})
	}
}

func TestReportTokenIsBoundToItsReport(t *testing.T) {
	a := ReportToken("install", "sec_aaaaaaaaaaaaaaaa")
	b := ReportToken("install", "sec_bbbbbbbbbbbbbbbb")
	if a == b {
		t.Fatal("two reports share a token")
	}
	if ReportToken("other-install", "sec_aaaaaaaaaaaaaaaa") == a {
		t.Fatal("token does not depend on the installation secret")
	}
}

func TestValidReportToken(t *testing.T) {
	const secret = "install"
	const id = "sec_abcdef0123456789"
	token := ReportToken(secret, id)

	tests := []struct {
		name   string
		secret string
		id     string
		token  string
		want   bool
	}{
		{name: "valid", secret: secret, id: id, token: token, want: true},
		{name: "wrong report", secret: secret, id: "sec_0000000000000000", token: token},
		{name: "wrong secret", secret: "other", id: id, token: token},
		{name: "empty token", secret: secret, id: id, token: ""},
		{name: "short token", secret: secret, id: id, token: token[:8]},
		{name: "no secret configured", secret: "", id: id, token: token},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidReportToken(tt.secret, tt.id, tt.token); got != tt.want {
				t.Errorf("ValidReportToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidReportTokenWithPreviousSecret(t *testing.T) {
	const id = "sec_abcdef0123456789"
	old := ReportToken("old", id)
	if !ValidReportTokenWithPrevious("new", "old", id, old) {
		t.Fatal("retained previous secret was rejected")
	}
	if ValidReportTokenWithPrevious("new", "", id, old) {
		t.Fatal("unretained previous secret was accepted")
	}
}

func TestReportTokenExpired(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		closedAt time.Time
		want     bool
	}{
		{name: "still open never expires", closedAt: time.Time{}},
		{name: "closed yesterday", closedAt: now.AddDate(0, 0, -1)},
		{name: "closed 29 days ago", closedAt: now.AddDate(0, 0, -29)},
		{name: "closed 31 days ago", closedAt: now.AddDate(0, 0, -31), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReportTokenExpired(tt.closedAt, now); got != tt.want {
				t.Errorf("ReportTokenExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}
