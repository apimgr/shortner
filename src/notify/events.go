package notify

import (
	"sort"

	"github.com/apimgr/shortner/src/config"
)

// The event ids of AI.md PART 17 "Default Templates". Each id is also the
// template file name (`{id}.tmpl`) and the key under
// `server.notifications.email.events`.
const (
	EventSecurityAlert    = "security_alert"
	EventBackupComplete   = "backup_complete"
	EventBackupFailed     = "backup_failed"
	EventSSLExpiring      = "ssl_expiring"
	EventSSLRenewed       = "ssl_renewed"
	EventSSLRenewalFailed = "ssl_renewal_failed"
	EventSchedulerError   = "scheduler_error"
	EventUpdateAvailable  = "update_available"
	EventUpdateInstalled  = "update_installed"
	EventTest             = "test"
)

// EventStartup and EventShutdown back the `startup`/`shutdown` switches
// that AI.md PART 17 "Configuration" lists under
// `server.notifications.email.events`. The spec's "Default Templates"
// table does not name a template for either (both default to false), so
// the two templates here follow the mandatory "Server Email Requirements"
// format and carry no event-specific variables beyond the globals.
const (
	EventStartup  = "startup"
	EventShutdown = "shutdown"
)

// EventNames returns every template/event id, sorted — the canonical list
// for the template editor and for `--maintenance` listings.
func EventNames() []string {
	names := make([]string, 0, len(eventVariables))
	for name := range eventVariables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// globalVariables is AI.md PART 17 "Global Variables (Available in All
// Templates)". Every template may use any of these.
var globalVariables = []string{
	"app_name",
	"app_url",
	"fqdn",
	"onion_url",
	"onion_address",
	"i2p_url",
	"i2p_address",
	"notification_reply_to",
	"timestamp",
	"year",
}

// eventVariables is AI.md PART 17 "Template-Specific Variables", keyed by
// event id. A template may use its own entries plus every global.
var eventVariables = map[string][]string{
	EventSecurityAlert:    {"event", "ip", "details"},
	EventBackupComplete:   {"filename", "size"},
	EventBackupFailed:     {"filename", "size", "error"},
	EventSSLExpiring:      {"expires_in", "expiry_date"},
	EventSSLRenewed:       {"expires_in", "expiry_date", "valid_until"},
	EventSSLRenewalFailed: {"error", "expires_in", "expiry_date", "next_retry"},
	EventSchedulerError:   {"task_name", "error", "next_run"},
	// AI.md PART 17 "Sane Defaults": update_available "Includes current
	// version, new version, and channel"; update_installed "Includes
	// previous version and new version".
	EventUpdateAvailable: {"current_version", "new_version", "channel"},
	EventUpdateInstalled: {"previous_version", "new_version"},
	EventTest:            {},
	EventStartup:         {"version", "mode"},
	EventShutdown:        {"version", "mode"},
}

// KnownVariables returns the set of variables legal in template name:
// every global plus that template's own. An unrecognized name yields the
// globals alone, which is what a brand-new custom template gets.
func KnownVariables(name string) map[string]bool {
	known := make(map[string]bool, len(globalVariables)+8)
	for _, v := range globalVariables {
		known[v] = true
	}
	for _, v := range eventVariables[name] {
		known[v] = true
	}
	return known
}

// EventEnabled reports whether the per-event switch under
// `server.notifications.email.events` allows this event, per AI.md
// PART 17 "Configuration".
//
// `test` has no switch: it is only ever sent by an explicit operator
// action (`email test`), which is itself the consent.
func EventEnabled(ev config.EmailEvents, event string) bool {
	switch event {
	case EventStartup:
		return ev.Startup
	case EventShutdown:
		return ev.Shutdown
	case EventBackupComplete:
		return ev.BackupComplete
	case EventBackupFailed:
		return ev.BackupFailed
	case EventSSLExpiring:
		return ev.SSLExpiring
	case EventSSLRenewed:
		return ev.SSLRenewed
	case EventSSLRenewalFailed:
		return ev.SSLRenewalFailed
	case EventSecurityAlert:
		return ev.SecurityAlert
	case EventSchedulerError:
		return ev.SchedulerError
	case EventUpdateAvailable:
		return ev.UpdateAvailable
	case EventUpdateInstalled:
		return ev.UpdateInstalled
	case EventTest:
		return true
	default:
		return false
	}
}
