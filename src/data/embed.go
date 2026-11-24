// Package data embeds application JSON assets into the binary when present,
// per AI.md PART 7 "Embedded Assets". The directory is currently empty.
package data

import "embed"

// FS embeds every file under this directory.
//
//go:embed all:*
var FS embed.FS
