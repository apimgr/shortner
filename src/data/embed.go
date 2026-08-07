// Package data embeds src/data/*.json application data files into every
// binary. See AI.md PART 7 "Embedded Assets" ("Application data (JSON
// files)"). No concrete data files are specified anywhere in AI.md PART
// 0-8 for this project — see TODO.AI.md for the deferred follow-up.
package data

import "embed"

// FS embeds every file under this directory, including the current
// .gitkeep placeholder — real *.json files land once a later PART defines
// their schema/content.
//
//go:embed all:*
var FS embed.FS
