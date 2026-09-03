package httpserver

import (
	"net"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{name: "public v4", ip: "93.184.216.34", want: true},
		{name: "public v6", ip: "2606:2800:220:1:248:1893:25c8:1946", want: true},
		{name: "loopback v4", ip: "127.0.0.1"},
		{name: "loopback v6", ip: "::1"},
		{name: "unspecified", ip: "0.0.0.0"},
		{name: "rfc1918 ten", ip: "10.0.0.5"},
		{name: "rfc1918 172", ip: "172.16.4.1"},
		{name: "rfc1918 192", ip: "192.168.1.1"},
		{name: "link local metadata", ip: "169.254.169.254"},
		{name: "link local v6", ip: "fe80::1"},
		{name: "unique local v6", ip: "fd00::1"},
		{name: "multicast", ip: "224.0.0.1"},
		{name: "carrier grade nat", ip: "100.64.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) = nil", tt.ip)
			}
			if got := isPublicIP(ip); got != tt.want {
				t.Errorf("isPublicIP(%s) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
	if isPublicIP(nil) {
		t.Error("isPublicIP(nil) = true, want false")
	}
}

func TestHostResolvesPublic(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr error
	}{
		{name: "public literal", host: "93.184.216.34"},
		{name: "loopback literal", host: "127.0.0.1", wantErr: errResearcherKeyPrivate},
		{name: "metadata literal", host: "169.254.169.254", wantErr: errResearcherKeyPrivate},
		{name: "empty host", host: "", wantErr: errResearcherKeyScheme},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hostResolvesPublic(tt.host)
			if err != tt.wantErr {
				t.Errorf("hostResolvesPublic(%q) = %v, want %v", tt.host, err, tt.wantErr)
			}
		})
	}
}

func TestFetchResearcherKeyRejectsUnsafeURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "http scheme", url: "http://example.com/key.asc"},
		{name: "file scheme", url: "file:///etc/passwd"},
		{name: "no host", url: "https:///key.asc"},
		{name: "loopback host", url: "https://127.0.0.1/key.asc"},
		{name: "metadata host", url: "https://169.254.169.254/latest/meta-data/"},
		{name: "private host", url: "https://10.1.2.3/key.asc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := fetchResearcherKey(tt.url); err == nil {
				t.Errorf("fetchResearcherKey(%q) error = nil, want a rejection", tt.url)
			}
		})
	}
}

func TestResearcherPGPKeyRejectsUnusableValues(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	tests := []struct {
		name     string
		supplied string
	}{
		{name: "empty"},
		{name: "not a key", supplied: "just some text"},
		{name: "bad armor", supplied: "-----BEGIN PGP PUBLIC KEY BLOCK-----\nnope\n-----END PGP PUBLIC KEY BLOCK-----"},
		{name: "private url", supplied: "https://127.0.0.1/key.asc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := fd.researcherPGPKey(tt.supplied); err == nil {
				t.Errorf("researcherPGPKey(%q) error = nil, want an error", tt.supplied)
			}
		})
	}
}

func TestProjectPGPKeyAbsentWithoutKeypair(t *testing.T) {
	fd := testSecurityFrontendDeps(t)
	if _, err := fd.projectPGPKey(); err == nil {
		t.Error("projectPGPKey() error = nil with no keypair generated")
	}
}
