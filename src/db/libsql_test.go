package db

import "testing"

func TestParseLibSQLTLS(t *testing.T) {
	tests := []struct {
		name       string
		scheme     string
		values     []string
		defaultTLS bool
		wantTLS    bool
		wantErr    bool
	}{
		{name: "libsql enabled synonym", scheme: "libsql", values: []string{"yes"}, defaultTLS: true, wantTLS: true},
		{name: "libsql disabled rejected", scheme: "libsql", values: []string{"no"}, defaultTLS: true, wantErr: true},
		{name: "https enabled", scheme: "https", values: []string{"enabled"}, defaultTLS: true, wantTLS: true},
		{name: "https disabled rejected", scheme: "https", values: []string{"off"}, defaultTLS: true, wantErr: true},
		{name: "http disabled", scheme: "http", values: []string{"off"}, defaultTLS: false, wantTLS: false},
		{name: "http enabled rejected", scheme: "http", values: []string{"on"}, defaultTLS: false, wantErr: true},
		{name: "empty uses scheme default", scheme: "https", values: []string{""}, defaultTLS: true, wantTLS: true},
		{name: "invalid value", scheme: "https", values: []string{"sometimes"}, defaultTLS: true, wantErr: true},
		{name: "duplicate values", scheme: "https", values: []string{"true", "true"}, defaultTLS: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTLS, err := parseLibSQLTLS(tt.scheme, tt.values, tt.defaultTLS)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseLibSQLTLS() error = %v, wantErr %t", err, tt.wantErr)
			}
			if err == nil && gotTLS != tt.wantTLS {
				t.Errorf("parseLibSQLTLS() = %t, want %t", gotTLS, tt.wantTLS)
			}
		})
	}
}
