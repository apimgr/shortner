package fqdn

import (
	"os"
	"testing"
)

func TestGetAllDomains(t *testing.T) {
	t.Setenv("DOMAIN", "")
	if got := GetAllDomains(); got != nil {
		t.Fatalf("expected nil for unset DOMAIN, got %v", got)
	}

	t.Setenv("DOMAIN", "example.com")
	if got := GetAllDomains(); len(got) != 1 || got[0] != "example.com" {
		t.Fatalf("single domain: got %v", got)
	}

	t.Setenv("DOMAIN", "myapp.com, www.myapp.com ,api.myapp.com")
	got := GetAllDomains()
	want := []string{"myapp.com", "www.myapp.com", "api.myapp.com"}
	if len(got) != len(want) {
		t.Fatalf("multi domain: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("multi domain[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestGetFQDN_DomainEnvTakesPriority(t *testing.T) {
	t.Setenv("DOMAIN", "api.example.com")
	if got := GetFQDN("shortner"); got != "api.example.com" {
		t.Fatalf("got %q, want api.example.com", got)
	}
}

func TestGetFQDN_FallsBackToHostname(t *testing.T) {
	t.Setenv("DOMAIN", "")
	os.Unsetenv("HOSTNAME")
	got := GetFQDN("shortner")
	if got == "" {
		t.Fatal("expected a non-empty FQDN fallback")
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"localhost":   true,
		"127.0.0.1":   true,
		"::1":         true,
		"example.com": false,
		"8.8.8.8":     false,
	}
	for host, want := range cases {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestIsDevTLD(t *testing.T) {
	cases := []struct {
		host        string
		projectName string
		want        bool
	}{
		{"shortner", "shortner", true},
		{"app.shortner", "shortner", true},
		{"foo.local", "shortner", true},
		{"foo.test", "shortner", true},
		{"foo.internal", "shortner", true},
		{"example.com", "shortner", false},
		{"api.example.com", "shortner", false},
		{"LOCALHOST", "shortner", true},
	}
	for _, c := range cases {
		if got := IsDevTLD(c.host, c.projectName); got != c.want {
			t.Errorf("IsDevTLD(%q, %q) = %v, want %v", c.host, c.projectName, got, c.want)
		}
	}
}
