package httpserver

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"testing"

	"github.com/apimgr/shortner/src/config"
)

func TestParsePortSetting(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
		bad   bool
	}{
		{name: "single", value: "8090", want: []string{"8090"}},
		{name: "dual", value: " 8090, 8443 ", want: []string{"8090", "8443"}},
		{name: "ephemeral", value: "0", want: []string{"0"}},
		{name: "too many", value: "80,443,8080", bad: true},
		{name: "empty", value: "", bad: true},
		{name: "invalid number", value: "http,443", bad: true},
		{name: "out of range", value: "65536", bad: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePortSetting(test.value)
			if test.bad {
				if err == nil {
					t.Fatalf("parsePortSetting(%q) succeeded, want error", test.value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parsePortSetting(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}

func TestServeUsesPreboundListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "prebound")
	})}
	done := make(chan error, 1)
	go func() { done <- serve(server, listener) }()

	response, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		_ = server.Close()
		t.Fatalf("request to prebound listener: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	_ = server.Close()
	if err := <-done; err != nil {
		t.Fatalf("serve with prebound listener: %v", err)
	}
}

func TestNewDualPortsMountsHTTPChallengeAndHTTPS(t *testing.T) {
	cfg := config.Default("")
	cfg.Server.Port = "8090,8443"
	challengeCalls := 0
	challenge := func(next http.Handler) http.Handler {
		challengeCalls++
		return next
	}
	srv := New(Options{Config: cfg, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, HTTPChallengeHandler: challenge})
	if srv.portErr != nil {
		t.Fatal(srv.portErr)
	}
	if len(srv.servers) != 2 {
		t.Fatalf("server listeners = %d, want HTTP and HTTPS", len(srv.servers))
	}
	if srv.servers[0].Addr != "[::]:8090" || srv.servers[0].TLSConfig != nil {
		t.Errorf("HTTP listener = addr %q TLS %v", srv.servers[0].Addr, srv.servers[0].TLSConfig)
	}
	if srv.servers[1].Addr != "[::]:8443" || srv.servers[1].TLSConfig == nil {
		t.Errorf("HTTPS listener = addr %q TLS %v", srv.servers[1].Addr, srv.servers[1].TLSConfig)
	}
	if challengeCalls != 1 {
		t.Errorf("HTTP-01 wrapper called %d times, want once for HTTP", challengeCalls)
	}
}

func TestNewRejectsDualPortsWithoutTLS(t *testing.T) {
	cfg := config.Default("")
	cfg.Server.Port = "8090,8443"
	srv := New(Options{Config: cfg})
	if srv.portErr == nil {
		t.Fatal("dual ports without TLS accepted")
	}
}
