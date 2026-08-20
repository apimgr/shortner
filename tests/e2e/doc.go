// Package e2e holds the browser end-to-end suite described in AI.md PART
// 28 "Browser End-to-End Testing". Every test file in this package is
// behind the `e2e` build tag so `go test ./...` — the commit gate — never
// tries to launch a browser; the suite runs only through tests/e2e.sh,
// which supplies the server binary and a Chrome DevTools endpoint.
//
// This file carries no build tag on purpose: a package whose every file is
// excluded by a build constraint makes `go test ./...` fail with "build
// constraints exclude all Go files".
package e2e
