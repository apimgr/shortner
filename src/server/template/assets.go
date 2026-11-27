package templateassets

import (
	"embed"
)

// EmailFS embeds the server's default email templates independently from
// the HTTP server package so the notification package stays acyclic.
//
//go:embed email/*.tmpl
var emailFS embed.FS

// ReadEmailTemplate returns one embedded email template by relative name.
func ReadEmailTemplate(name string) ([]byte, error) {
	return emailFS.ReadFile(name)
}
