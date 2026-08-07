package main

import "testing"

func TestFilterDaemonFlag(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"removes daemon flag", []string{"--debug", "--daemon", "--port", "8080"}, []string{"--debug", "--port", "8080"}},
		{"no daemon flag present", []string{"--debug"}, []string{"--debug"}},
		{"only daemon flag", []string{"--daemon"}, []string{}},
		{"empty args", []string{}, []string{}},
		{"multiple daemon flags all removed", []string{"--daemon", "--debug", "--daemon"}, []string{"--debug"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterDaemonFlag(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("filterDaemonFlag(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("filterDaemonFlag(%v) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestShouldDaemonize(t *testing.T) {
	tests := []struct {
		name            string
		isServiceStart  bool
		daemonFlag      bool
		configDaemonize bool
		want            bool
	}{
		{"daemon flag alone", false, true, false, true},
		{"config daemonize alone", false, false, true, true},
		{"neither flag nor config", false, false, false, false},
		{"daemon flag overrides false config", false, true, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldDaemonize(tt.isServiceStart, tt.daemonFlag, tt.configDaemonize)
			if got != tt.want {
				t.Errorf("shouldDaemonize(%v,%v,%v) = %v, want %v",
					tt.isServiceStart, tt.daemonFlag, tt.configDaemonize, got, tt.want)
			}
		})
	}
}

// TestShouldDaemonizeServiceStartBranch exercises the isServiceStart
// branch, which calls the real detectServiceManager() (environment
// dependent). It only asserts the result is consistent with what
// detectServiceManager() itself reports, rather than a fixed expectation.
func TestShouldDaemonizeServiceStartBranch(t *testing.T) {
	mgr := detectServiceManager()
	want := mgr == "sysv" || mgr == "rcd"

	got := shouldDaemonize(true, false, false)
	if got != want {
		t.Errorf("shouldDaemonize(isServiceStart=true) = %v, want %v (detected manager %q)", got, want, mgr)
	}
}
