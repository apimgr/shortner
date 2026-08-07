// See AI.md PART 7 "Embedded Assets" and PART 16 "Embedding Templates".
package server

import "embed"

// TemplateFS embeds every file under template/, including the current
// .gitkeep placeholder — real .tmpl files land with PART 16 (web frontend).
//
//go:embed all:template
var TemplateFS embed.FS

// StaticFS embeds every file under static/, including the current
// .gitkeep placeholder — real static assets land with PART 16.
//
//go:embed all:static
var StaticFS embed.FS
