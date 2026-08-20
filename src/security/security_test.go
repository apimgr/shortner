package security

import (
	"strings"
	"testing"
)

func TestGenerateTokenFormat(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}
	if !strings.HasPrefix(tok, "tok_") {
		t.Errorf("token = %q, want tok_ prefix", tok)
	}
	if len(tok) != 4+TokenLength {
		t.Errorf("len(token) = %d, want %d", len(tok), 4+TokenLength)
	}
}

func TestGenerateTokenUnique(t *testing.T) {
	a, _ := GenerateToken()
	b, _ := GenerateToken()
	if a == b {
		t.Errorf("two generated tokens were identical: %q", a)
	}
}

func TestHashTokenDeterministic(t *testing.T) {
	first := HashToken("abc")
	second := HashToken("abc")
	if first != second {
		t.Errorf("HashToken not deterministic")
	}
	if HashToken("abc") == HashToken("abd") {
		t.Errorf("HashToken collision for different input")
	}
}

func TestTokenPrefix(t *testing.T) {
	if got := TokenPrefix("tok_abcdefghijklmno"); got != "tok_abcdefgh" {
		t.Errorf("TokenPrefix = %q, want tok_abcdefgh", got)
	}
	if got := TokenPrefix("short"); got != "short" {
		t.Errorf("TokenPrefix(short) = %q, want short", got)
	}
}

func TestCompareTokenHash(t *testing.T) {
	raw := "tok_test123"
	hash := HashToken(raw)
	if !CompareTokenHash(raw, hash) {
		t.Errorf("CompareTokenHash(raw, hash) = false, want true")
	}
	if CompareTokenHash("wrong", hash) {
		t.Errorf("CompareTokenHash(wrong, hash) = true, want false")
	}
	if CompareTokenHash(raw, "not-a-valid-hash") {
		t.Errorf("CompareTokenHash with malformed stored hash = true, want false")
	}
}

func TestCompareServerToken(t *testing.T) {
	if !CompareServerToken("tok_operator", "tok_operator") {
		t.Errorf("CompareServerToken matching = false, want true")
	}
	if CompareServerToken("tok_wrong", "tok_operator") {
		t.Errorf("CompareServerToken mismatch = true, want false")
	}
	if CompareServerToken("anything", "") {
		t.Errorf("CompareServerToken with empty server token = true, want false")
	}
}

func TestGenerateSecretLength(t *testing.T) {
	secret, err := GenerateSecret(32)
	if err != nil {
		t.Fatalf("GenerateSecret() error = %v", err)
	}
	if len(secret) != 32 {
		t.Errorf("len(secret) = %d, want 32", len(secret))
	}
}

func TestHashAndVerifyPassword(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	ok, err := VerifyPassword("correct horse battery staple", encoded)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Errorf("VerifyPassword(correct) = false, want true")
	}

	ok, err = VerifyPassword("wrong password", encoded)
	if err != nil {
		t.Fatalf("VerifyPassword(wrong) error = %v", err)
	}
	if ok {
		t.Errorf("VerifyPassword(wrong) = true, want false")
	}
}

func TestVerifyPasswordRejectsMalformedEncoding(t *testing.T) {
	if _, err := VerifyPassword("x", "not-argon2id"); err == nil {
		t.Errorf("VerifyPassword() error = nil, want error for malformed encoding")
	}
}

func TestAnonymizeIPv4(t *testing.T) {
	got := AnonymizeIP("203.0.113.42")
	want := "203.0.113.0"
	if got != want {
		t.Errorf("AnonymizeIP(v4) = %q, want %q", got, want)
	}
}

func TestAnonymizeIPv6(t *testing.T) {
	// Zeroes the last 80 bits (10 bytes / 5 groups), keeping a /48
	// prefix (3 groups) — see security/anonymize.go.
	got := AnonymizeIP("2001:db8:85a3:1111:2222:3333:4444:5555")
	want := "2001:db8:85a3::"
	if got != want {
		t.Errorf("AnonymizeIP(v6) = %q, want %q", got, want)
	}
}

func TestAnonymizeIPInvalid(t *testing.T) {
	if got := AnonymizeIP("not-an-ip"); got != "" {
		t.Errorf("AnonymizeIP(invalid) = %q, want empty", got)
	}
}

func TestGenerateShortCodeFormat(t *testing.T) {
	code, err := GenerateShortCode()
	if err != nil {
		t.Fatalf("GenerateShortCode() error = %v", err)
	}
	if len(code) != ShortCodeLength {
		t.Errorf("len(code) = %d, want %d", len(code), ShortCodeLength)
	}
	if !ValidateSlugFormat(code) {
		t.Errorf("generated code %q fails ValidateSlugFormat", code)
	}
}

func TestValidateSlugFormat(t *testing.T) {
	cases := map[string]bool{
		"abc":                                  true,
		"my-link":                              true,
		"a1-b2-c3":                             true,
		"ab":                                   false, // too short
		"this-slug-is-way-too-long-for-limits": false,
		"has space":                            false,
		"has_underscore":                       false,
	}
	for slug, want := range cases {
		if got := ValidateSlugFormat(slug); got != want {
			t.Errorf("ValidateSlugFormat(%q) = %v, want %v", slug, got, want)
		}
	}
}

func TestIsReservedSlug(t *testing.T) {
	if !IsReservedSlug("API") {
		t.Errorf("IsReservedSlug(API) = false, want true (case-insensitive)")
	}
	if IsReservedSlug("my-custom-link") {
		t.Errorf("IsReservedSlug(my-custom-link) = true, want false")
	}
}

// TestRandomStringIsUnbiased guards the rejection sampling in
// RandomString. Mapping a raw crypto/rand byte with `%% len(alphabet)`
// is only uniform when the alphabet size divides 256; for base62 it does
// not (256 = 4*62 + 8), which over-selects the first 8 characters by 25%%.
// A chi-squared-style spread check catches a regression to modulo mapping.
func TestRandomStringIsUnbiased(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const draws = 200000

	out, err := RandomString(alphabet, draws)
	if err != nil {
		t.Fatalf("RandomString() error = %v", err)
	}
	if len(out) != draws {
		t.Fatalf("len = %d, want %d", len(out), draws)
	}

	counts := map[rune]int{}
	for _, r := range out {
		counts[r]++
	}
	if len(counts) != len(alphabet) {
		t.Errorf("distinct characters = %d, want %d", len(counts), len(alphabet))
	}

	expected := float64(draws) / float64(len(alphabet))
	// Modulo bias would push the first 8 characters to ~1.25x expected;
	// 10% is comfortably inside sampling noise at this sample size but
	// well below that.
	for r, n := range counts {
		deviation := (float64(n) - expected) / expected
		if deviation > 0.10 || deviation < -0.10 {
			t.Errorf("character %q drawn %d times, expected ~%.0f (deviation %.2f)", r, n, expected, deviation)
		}
	}
}

func TestRandomStringRejectsBadAlphabet(t *testing.T) {
	if _, err := RandomString("", 4); err == nil {
		t.Error("RandomString(empty alphabet) error = nil, want error")
	}
}
