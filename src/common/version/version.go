// Package version holds the build information shared by the server and CLI
// binaries so both link the same -ldflags-populated values instead of each
// redeclaring them locally. See AI.md PART 7 "Common Go Modules" (version
// package) and PART 25 (Makefile LDFLAGS).
package version

import (
	"strconv"
	"time"
)

// Version, CommitID, BuildEpoch, and OfficialSite are set via -ldflags at
// build time (see Makefile LDFLAGS). Their zero values are used for `go
// run`/`go test` and other non-release builds.
var (
	Version  = "devel"
	CommitID = "N/A"
	// BuildDate is derived from BuildEpoch in init(); "N/A" when BuildEpoch is unset
	BuildDate = "N/A"
	// BuildEpoch is the Unix build timestamp (seconds, UTC) set via -ldflags; "0" when unset
	BuildEpoch   = "0"
	OfficialSite = ""
)

// Epoch parses the embedded BuildEpoch ldflag; 0 when unset or invalid.
// The updater's daily-channel check compares the rolling "daily" release
// against this binary's own build time.
func Epoch() int64 {
	n, err := strconv.ParseInt(BuildEpoch, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// init derives BuildDate (RFC 3339 UTC) from the embedded BuildEpoch.
func init() {
	if n := Epoch(); n > 0 {
		BuildDate = time.Unix(n, 0).UTC().Format("2006-01-02T15:04:05Z")
	}
}

// String returns the plain version string (e.g. "1.2.3").
func String() string {
	return Version
}

// Full returns a one-line "version (commit, built date)" summary suitable
// for --version output and startup banners.
func Full() string {
	return Version + " (" + CommitID + ", built " + BuildDate + ")"
}
