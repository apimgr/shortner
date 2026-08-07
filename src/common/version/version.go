// Package version holds the build information shared by the server and CLI
// binaries so both link the same -ldflags-populated values instead of each
// redeclaring them locally. See AI.md PART 7 "Common Go Modules" (version
// package) and PART 25 (Makefile LDFLAGS).
package version

// Version, CommitID, BuildDate, and OfficialSite are set via -ldflags at
// build time (see Makefile LDFLAGS). Their zero values are used for `go
// run`/`go test` and other non-release builds.
var (
	Version      = "devel"
	CommitID     = "N/A"
	BuildDate    = "N/A"
	OfficialSite = ""
)

// String returns the plain version string (e.g. "1.2.3").
func String() string {
	return Version
}

// Full returns a one-line "version (commit, built date)" summary suitable
// for --version output and startup banners.
func Full() string {
	return Version + " (" + CommitID + ", built " + BuildDate + ")"
}
