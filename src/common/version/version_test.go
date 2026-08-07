package version

import (
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	old := Version
	defer func() { Version = old }()

	Version = "1.2.3"
	if got := String(); got != "1.2.3" {
		t.Errorf("String() = %q, want %q", got, "1.2.3")
	}
}

func TestFull(t *testing.T) {
	oldV, oldC, oldD := Version, CommitID, BuildDate
	defer func() { Version, CommitID, BuildDate = oldV, oldC, oldD }()

	tests := []struct {
		name    string
		version string
		commit  string
		date    string
	}{
		{"devel defaults", "devel", "N/A", "N/A"},
		{"release values", "1.4.0", "abc123", "2026-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version, CommitID, BuildDate = tt.version, tt.commit, tt.date
			got := Full()
			if !strings.Contains(got, tt.version) {
				t.Errorf("Full() = %q, want to contain version %q", got, tt.version)
			}
			if !strings.Contains(got, tt.commit) {
				t.Errorf("Full() = %q, want to contain commit %q", got, tt.commit)
			}
			if !strings.Contains(got, tt.date) {
				t.Errorf("Full() = %q, want to contain date %q", got, tt.date)
			}
			want := tt.version + " (" + tt.commit + ", built " + tt.date + ")"
			if got != want {
				t.Errorf("Full() = %q, want %q", got, want)
			}
		})
	}
}
