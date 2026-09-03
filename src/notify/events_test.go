package notify

import (
	"testing"

	"github.com/apimgr/shortner/src/config"
)

func TestEventNamesCoverEveryTemplate(t *testing.T) {
	names := EventNames()
	if len(names) != len(eventVariables) {
		t.Fatalf("EventNames() returned %d names, want %d", len(names), len(eventVariables))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("EventNames() is not sorted: %v", names)
		}
	}
	// Every event must have a template on disk, or a send would silently
	// fail at runtime rather than at build time.
	s := &Store{}
	for _, name := range names {
		if _, err := s.Load(name); err != nil {
			t.Errorf("no embedded template for event %q: %v", name, err)
		}
	}
}

func TestKnownVariables(t *testing.T) {
	tests := []struct {
		name      string
		event     string
		wantTrue  []string
		wantFalse []string
	}{
		{
			name:      "security_alert",
			event:     EventSecurityAlert,
			wantTrue:  []string{"event", "ip", "details", "app_name", "year"},
			wantFalse: []string{"task_name", "filename"},
		},
		{
			name:      "scheduler_error",
			event:     EventSchedulerError,
			wantTrue:  []string{"task_name", "error", "next_run", "fqdn"},
			wantFalse: []string{"ip", "filename"},
		},
		{
			name:      "unknown event still gets the globals",
			event:     "not_a_real_event",
			wantTrue:  []string{"app_name", "app_url", "fqdn", "onion_url", "i2p_url", "timestamp"},
			wantFalse: []string{"error"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			known := KnownVariables(tc.event)
			for _, v := range tc.wantTrue {
				if !known[v] {
					t.Errorf("KnownVariables(%s)[%q] = false, want true", tc.event, v)
				}
			}
			for _, v := range tc.wantFalse {
				if known[v] {
					t.Errorf("KnownVariables(%s)[%q] = true, want false", tc.event, v)
				}
			}
		})
	}
}

func TestEventEnabled(t *testing.T) {
	all := config.EmailEvents{
		Startup:          true,
		Shutdown:         true,
		BackupComplete:   true,
		BackupFailed:     true,
		SSLExpiring:      true,
		SSLRenewed:       true,
		SSLRenewalFailed: true,
		SecurityAlert:    true,
		SchedulerError:   true,
		UpdateAvailable:  true,
		UpdateInstalled:  true,
	}

	tests := []struct {
		name  string
		ev    config.EmailEvents
		event string
		want  bool
	}{
		{"all on: security_alert", all, EventSecurityAlert, true},
		{"all on: backup_failed", all, EventBackupFailed, true},
		{"all on: update_installed", all, EventUpdateInstalled, true},
		{"all off: security_alert", config.EmailEvents{}, EventSecurityAlert, false},
		{"all off: scheduler_error", config.EmailEvents{}, EventSchedulerError, false},
		{"test has no switch", config.EmailEvents{}, EventTest, true},
		{"unknown event is never enabled", all, "not_a_real_event", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EventEnabled(tc.ev, tc.event); got != tc.want {
				t.Errorf("EventEnabled(%s) = %v, want %v", tc.event, got, tc.want)
			}
		})
	}
}

func TestEveryEventHasASwitch(t *testing.T) {
	all := config.EmailEvents{
		Startup:          true,
		Shutdown:         true,
		BackupComplete:   true,
		BackupFailed:     true,
		SSLExpiring:      true,
		SSLRenewed:       true,
		SSLRenewalFailed: true,
		SecurityAlert:    true,
		SchedulerError:   true,
		UpdateAvailable:  true,
		UpdateInstalled:  true,
	}
	for _, name := range EventNames() {
		if !EventEnabled(all, name) {
			t.Errorf("event %q has no entry in EventEnabled", name)
		}
	}
}
