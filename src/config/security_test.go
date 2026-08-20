package config

import (
	"strings"
	"testing"
)

func TestNormalizeCIDRBareIPv4ExpandsToSlash32(t *testing.T) {
	got, err := NormalizeCIDR("203.0.113.5")
	if err != nil {
		t.Fatalf("NormalizeCIDR() error = %v", err)
	}
	if got != "203.0.113.5/32" {
		t.Errorf("NormalizeCIDR() = %q, want 203.0.113.5/32", got)
	}
}

func TestNormalizeCIDRBareIPv6ExpandsToSlash128(t *testing.T) {
	got, err := NormalizeCIDR("2001:db8::1")
	if err != nil {
		t.Fatalf("NormalizeCIDR() error = %v", err)
	}
	if got != "2001:db8::1/128" {
		t.Errorf("NormalizeCIDR() = %q, want 2001:db8::1/128", got)
	}
}

func TestNormalizeCIDRAcceptsExplicitPrefix(t *testing.T) {
	got, err := NormalizeCIDR("203.0.113.0/24")
	if err != nil {
		t.Fatalf("NormalizeCIDR() error = %v", err)
	}
	if got != "203.0.113.0/24" {
		t.Errorf("NormalizeCIDR() = %q, want 203.0.113.0/24", got)
	}
}

func TestNormalizeCIDRRejectsEmpty(t *testing.T) {
	if _, err := NormalizeCIDR(""); err == nil {
		t.Error("NormalizeCIDR(empty) error = nil, want error")
	}
	if _, err := NormalizeCIDR("   "); err == nil {
		t.Error("NormalizeCIDR(whitespace) error = nil, want error")
	}
}

func TestNormalizeCIDRRejectsInvalidIP(t *testing.T) {
	if _, err := NormalizeCIDR("not-an-ip"); err == nil {
		t.Error("NormalizeCIDR(invalid IP) error = nil, want error")
	}
}

func TestNormalizeCIDRRejectsInvalidCIDR(t *testing.T) {
	if _, err := NormalizeCIDR("203.0.113.0/notanumber"); err == nil {
		t.Error("NormalizeCIDR(invalid CIDR) error = nil, want error")
	}
}

func TestNormalizeCIDRRejectsOverlyBroadIPv4(t *testing.T) {
	if _, err := NormalizeCIDR("0.0.0.0/0"); err == nil {
		t.Error("NormalizeCIDR(/0 IPv4) error = nil, want error")
	}
	if _, err := NormalizeCIDR("10.0.0.0/7"); err == nil {
		t.Error("NormalizeCIDR(/7 IPv4) error = nil, want error")
	}
	// /8 is the minimum accepted width.
	if _, err := NormalizeCIDR("10.0.0.0/8"); err != nil {
		t.Errorf("NormalizeCIDR(/8 IPv4) error = %v, want nil", err)
	}
}

func TestNormalizeCIDRRejectsOverlyBroadIPv6(t *testing.T) {
	if _, err := NormalizeCIDR("::/0"); err == nil {
		t.Error("NormalizeCIDR(/0 IPv6) error = nil, want error")
	}
	if _, err := NormalizeCIDR("2001:db8::/15"); err == nil {
		t.Error("NormalizeCIDR(/15 IPv6) error = nil, want error")
	}
	// /16 is the minimum accepted width.
	if _, err := NormalizeCIDR("2001::/16"); err != nil {
		t.Errorf("NormalizeCIDR(/16 IPv6) error = %v, want nil", err)
	}
}

func TestValidateSecurityDropsInvalidAllowlistAndBlockedEntries(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Security.Allowlist = []AllowlistEntry{
		{CIDR: "203.0.113.5", Description: "ok"},
		{CIDR: "garbage", Description: "bad"},
	}
	cfg.Server.Security.BlockedIPs = []BlockedIPEntry{
		{CIDR: "198.51.100.9", Reason: "ok"},
		{CIDR: "0.0.0.0/0", Reason: "too broad"},
	}

	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want warnings for the dropped entries")
	}
	if len(cfg.Server.Security.Allowlist) != 1 || cfg.Server.Security.Allowlist[0].CIDR != "203.0.113.5/32" {
		t.Errorf("Allowlist = %+v, want only the normalized valid entry", cfg.Server.Security.Allowlist)
	}
	if len(cfg.Server.Security.BlockedIPs) != 1 || cfg.Server.Security.BlockedIPs[0].CIDR != "198.51.100.9/32" {
		t.Errorf("BlockedIPs = %+v, want only the normalized valid entry", cfg.Server.Security.BlockedIPs)
	}
}

func TestValidateSecurityAbuseDetectionDefaults(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Security.AbuseDetection.RequestFlood.Multiplier = 1
	cfg.Server.Security.AbuseDetection.RequestFlood.BlockDuration = "bogus"

	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want warnings for invalid abuse detection settings")
	}
	def := Default("/db")
	if cfg.Server.Security.AbuseDetection.RequestFlood.Multiplier != def.Server.Security.AbuseDetection.RequestFlood.Multiplier {
		t.Errorf("Multiplier = %d, want default %d", cfg.Server.Security.AbuseDetection.RequestFlood.Multiplier, def.Server.Security.AbuseDetection.RequestFlood.Multiplier)
	}
	if cfg.Server.Security.AbuseDetection.RequestFlood.BlockDuration != def.Server.Security.AbuseDetection.RequestFlood.BlockDuration {
		t.Errorf("BlockDuration = %q, want default %q", cfg.Server.Security.AbuseDetection.RequestFlood.BlockDuration, def.Server.Security.AbuseDetection.RequestFlood.BlockDuration)
	}
}

func TestValidateSecurityNeverFailsStartup(t *testing.T) {
	// Per the Config Validation Rule: invalid config warns and defaults,
	// it never returns an error or panics.
	cfg := Default("/db")
	cfg.Server.Security.Allowlist = []AllowlistEntry{{CIDR: "totally invalid"}}
	cfg.Server.Security.AbuseDetection.RequestFlood.Multiplier = -5
	_ = Validate(cfg)
}

func TestValidateWebHeadersHSTS(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.HSTS.MaxAgeSeconds = -1
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning for negative HSTS max age")
	}
	def := Default("/db")
	if cfg.Web.HSTS.MaxAgeSeconds != def.Web.HSTS.MaxAgeSeconds {
		t.Errorf("HSTS.MaxAgeSeconds = %d, want default %d", cfg.Web.HSTS.MaxAgeSeconds, def.Web.HSTS.MaxAgeSeconds)
	}
}

func TestValidateWebHeadersHSTSDisabledZero(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.HSTS.Enabled = true
	cfg.Web.HSTS.MaxAgeSeconds = 0
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning that HSTS is disabled")
	}
	// A max age of 0 is legal — it stays 0, only a warning is emitted.
	if cfg.Web.HSTS.MaxAgeSeconds != 0 {
		t.Errorf("HSTS.MaxAgeSeconds = %d, want unchanged 0", cfg.Web.HSTS.MaxAgeSeconds)
	}
}

func TestValidateWebHeadersHSTSBelowPreloadThreshold(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.HSTS.Enabled = true
	cfg.Web.HSTS.MaxAgeSeconds = 3600
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a preload-ineligibility warning")
	}
	// Below-threshold values are accepted as written, only warned about.
	if cfg.Web.HSTS.MaxAgeSeconds != 3600 {
		t.Errorf("HSTS.MaxAgeSeconds = %d, want unchanged 3600", cfg.Web.HSTS.MaxAgeSeconds)
	}
}

func TestValidateWebHeadersEnumFields(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.Headers.COOP = "bogus"
	cfg.Web.Headers.COEP = "bogus"
	cfg.Web.Headers.CORP = "bogus"
	cfg.Web.Headers.CrossDomainPolicies = "bogus"

	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want warnings for invalid enum fields")
	}
	def := Default("/db")
	if cfg.Web.Headers.COOP != def.Web.Headers.COOP {
		t.Errorf("COOP = %q, want default %q", cfg.Web.Headers.COOP, def.Web.Headers.COOP)
	}
	if cfg.Web.Headers.COEP != def.Web.Headers.COEP {
		t.Errorf("COEP = %q, want default %q", cfg.Web.Headers.COEP, def.Web.Headers.COEP)
	}
	if cfg.Web.Headers.CORP != def.Web.Headers.CORP {
		t.Errorf("CORP = %q, want default %q", cfg.Web.Headers.CORP, def.Web.Headers.CORP)
	}
	if cfg.Web.Headers.CrossDomainPolicies != def.Web.Headers.CrossDomainPolicies {
		t.Errorf("CrossDomainPolicies = %q, want default %q", cfg.Web.Headers.CrossDomainPolicies, def.Web.Headers.CrossDomainPolicies)
	}
}

func TestValidateWebHeadersDNSPrefetchControl(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.Headers.DNSPrefetchControl = "bogus"
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning for invalid dns_prefetch_control")
	}
	if cfg.Web.Headers.DNSPrefetchControl != "" {
		t.Errorf("DNSPrefetchControl = %q, want empty (header omitted)", cfg.Web.Headers.DNSPrefetchControl)
	}

	for _, valid := range []string{"", "on", "off"} {
		cfg := Default("/db")
		cfg.Web.Headers.DNSPrefetchControl = valid
		if warnings := Validate(cfg); anyContains(warnings, "dns_prefetch_control") {
			t.Errorf("Validate() warned for valid dns_prefetch_control %q: %v", valid, warnings)
		}
	}
}

func TestValidateWebHeadersNELSampleRate(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.Headers.NEL.SampleRate = 1.5
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning for out-of-range sample rate")
	}
	def := Default("/db")
	if cfg.Web.Headers.NEL.SampleRate != def.Web.Headers.NEL.SampleRate {
		t.Errorf("NEL.SampleRate = %v, want default %v", cfg.Web.Headers.NEL.SampleRate, def.Web.Headers.NEL.SampleRate)
	}
}

func TestValidateWebHeadersNELMaxAge(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.Headers.NEL.MaxAgeSeconds = -1
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning for negative NEL max age")
	}
	def := Default("/db")
	if cfg.Web.Headers.NEL.MaxAgeSeconds != def.Web.Headers.NEL.MaxAgeSeconds {
		t.Errorf("NEL.MaxAgeSeconds = %d, want default %d", cfg.Web.Headers.NEL.MaxAgeSeconds, def.Web.Headers.NEL.MaxAgeSeconds)
	}
}

func TestValidateWebHeadersCSPMode(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.CSP.Mode = "bogus"
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning for invalid csp.mode")
	}
	def := Default("/db")
	if cfg.Web.CSP.Mode != def.Web.CSP.Mode {
		t.Errorf("CSP.Mode = %q, want default %q", cfg.Web.CSP.Mode, def.Web.CSP.Mode)
	}
}

func TestValidateWebHeadersCSPReportsSampleRate(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.CSP.ReportsSampleRate = -0.1
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning for negative reports_sample_rate")
	}
	def := Default("/db")
	if cfg.Web.CSP.ReportsSampleRate != def.Web.CSP.ReportsSampleRate {
		t.Errorf("CSP.ReportsSampleRate = %v, want default %v", cfg.Web.CSP.ReportsSampleRate, def.Web.CSP.ReportsSampleRate)
	}
}

func TestValidateWebHeadersReportsRateLimits(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.Reports.RateLimitPerMinute = 0
	cfg.Web.Reports.RateLimitPerIPBurst = -1
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want warnings for invalid report rate limits")
	}
	def := Default("/db")
	if cfg.Web.Reports.RateLimitPerMinute != def.Web.Reports.RateLimitPerMinute {
		t.Errorf("Reports.RateLimitPerMinute = %d, want default %d", cfg.Web.Reports.RateLimitPerMinute, def.Web.Reports.RateLimitPerMinute)
	}
	if cfg.Web.Reports.RateLimitPerIPBurst != def.Web.Reports.RateLimitPerIPBurst {
		t.Errorf("Reports.RateLimitPerIPBurst = %d, want default %d", cfg.Web.Reports.RateLimitPerIPBurst, def.Web.Reports.RateLimitPerIPBurst)
	}
}

func TestValidateWebHeadersPermissionsPolicyDefaultedWhenEmpty(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.PermissionsPolicy = nil
	Validate(cfg)
	if len(cfg.Web.PermissionsPolicy) == 0 {
		t.Error("PermissionsPolicy still empty after Validate(), want defaults populated")
	}
}

func TestValidateWebHeadersWellKnownUnsupportedBehaviorFixedAt404(t *testing.T) {
	cfg := Default("/db")
	cfg.Web.WellKnown.UnsupportedBehavior = 410
	warnings := Validate(cfg)
	if len(warnings) == 0 {
		t.Fatal("Validate() returned no warnings, want a warning that unsupported_behavior is fixed")
	}
	if cfg.Web.WellKnown.UnsupportedBehavior != 404 {
		t.Errorf("WellKnown.UnsupportedBehavior = %d, want 404", cfg.Web.WellKnown.UnsupportedBehavior)
	}
}

func TestValidateWebHeadersAcceptsDefaults(t *testing.T) {
	cfg := Default("/db")
	if warnings := Validate(cfg); len(warnings) != 0 {
		t.Errorf("Validate(default config) = %v, want no warnings", warnings)
	}
}

func TestEnsureEncryptionKeyGeneratesWhenEmpty(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Security.EncryptionKey = ""

	generated, err := EnsureEncryptionKey(cfg)
	if err != nil {
		t.Fatalf("EnsureEncryptionKey() error = %v", err)
	}
	if !generated {
		t.Error("EnsureEncryptionKey() generated = false, want true for an empty key")
	}
	if cfg.Server.Security.EncryptionKey == "" {
		t.Error("EncryptionKey still empty after EnsureEncryptionKey()")
	}

	key, err := DecodeEncryptionKey(cfg)
	if err != nil {
		t.Fatalf("DecodeEncryptionKey() error = %v", err)
	}
	if len(key) != 32 {
		t.Errorf("len(DecodeEncryptionKey()) = %d, want 32", len(key))
	}
}

func TestEnsureEncryptionKeyLeavesExistingKeyAlone(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Security.EncryptionKey = "existing-key-value"

	generated, err := EnsureEncryptionKey(cfg)
	if err != nil {
		t.Fatalf("EnsureEncryptionKey() error = %v", err)
	}
	if generated {
		t.Error("EnsureEncryptionKey() generated = true, want false when a key already exists")
	}
	if cfg.Server.Security.EncryptionKey != "existing-key-value" {
		t.Errorf("EncryptionKey = %q, want unchanged", cfg.Server.Security.EncryptionKey)
	}
}

func TestDecodeEncryptionKeyRejectsInvalidBase64(t *testing.T) {
	cfg := Default("/db")
	cfg.Server.Security.EncryptionKey = "not valid base64!!!"
	if _, err := DecodeEncryptionKey(cfg); err == nil {
		t.Error("DecodeEncryptionKey() error = nil, want error for invalid base64")
	}
}

func TestDecodeEncryptionKeyRejectsWrongLength(t *testing.T) {
	cfg := Default("/db")
	// Valid base64, but decodes to far fewer than 32 bytes.
	cfg.Server.Security.EncryptionKey = "YWJj"
	if _, err := DecodeEncryptionKey(cfg); err == nil {
		t.Error("DecodeEncryptionKey() error = nil, want error for wrong decoded length")
	}
}

// anyContains reports whether any warning in warnings contains sub.
func anyContains(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
