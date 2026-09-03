package cmd

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/apimgr/shortner/src/common/i18n"
	"github.com/apimgr/shortner/src/common/version"
)

// helpSyntaxColumn is the width of the flag-syntax column; descriptions
// start after it, separated by "- ", matching the server's --help layout.
const helpSyntaxColumn = 39

// helpEntry is one line of --help: the literal syntax plus the translation
// key holding its description.
type helpEntry struct {
	syntax string
	key    string
}

// helpSection is one titled block of --help.
type helpSection struct {
	titleKey string
	entries  []helpEntry
}

// helpSections mirrors AI.md PART 32 "--help Output" section for section.
// Flag syntax itself is never translated — it is part of the command-line
// contract, not prose.
var helpSections = []helpSection{
	{
		titleKey: "cli.information",
		entries: []helpEntry{
			{"-h, --help", "cli.flag_help"},
			{"-v, --version", "cli.flag_version"},
		},
	},
	{
		titleKey: "cli.shell_integration",
		entries: []helpEntry{
			{"--shell completions [SHELL]", "cli.flag_shell"},
			{"--shell init [SHELL]", "cli.shell_init"},
			{"--shell help", "cli.shell_help"},
		},
	},
	{
		titleKey: "client.connection",
		entries: []helpEntry{
			{"--server URL", "client.flag_server"},
			{"--token TOKEN", "client.flag_token"},
			{"--token-file FILE", "client.flag_token_file"},
			{"--config NAME", "client.flag_config"},
			{"--debug", "cli.flag_debug"},
			{"--color {auto|yes|no}", "cli.flag_color"},
			{"--lang CODE", "cli.flag_lang"},
			{"--update [check|yes]", "client.flag_update"},
		},
	},
	{
		titleKey: "client.output",
		entries: []helpEntry{
			{"--output {table|json|yaml|plain|csv}", "client.flag_output"},
			{"--quiet", "client.flag_quiet"},
			{"--verbose", "client.flag_verbose"},
		},
	},
	{
		titleKey: "client.commands",
		entries: []helpEntry{
			{"shorten URL [--slug NAME] [--expire WHEN]", "client.cmd_shorten"},
			{"get SLUG", "client.cmd_get"},
			{"list [--page N] [--limit N]", "client.cmd_list"},
			{"update SLUG [--url URL] [--expire WHEN]", "client.cmd_update"},
			{"delete SLUG [--force]", "client.cmd_delete"},
			{"stats SLUG", "client.cmd_stats"},
			{"health", "client.cmd_health"},
			{"setup", "client.cmd_setup"},
		},
	},
}

// supportedShells is the shell list printed by --help and accepted by
// --shell, from AI.md PART 32 "Shell Completions".
var supportedShells = []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "powershell", "pwsh"}

// helpText builds the full --help output for binaryName in the selected
// output language.
func helpText(binaryName, lang string) string {
	var b strings.Builder

	b.WriteString(i18n.TranslateFormat(lang, "client.description", map[string]string{
		"binary":          binaryName,
		"project_version": version.Version,
		"project_name":    ProjectName,
	}))
	b.WriteString("\n\n")

	b.WriteString(i18n.Translate(lang, "cli.usage"))
	b.WriteString("\n  " + binaryName + " [command] [args] [flags]\n")
	b.WriteString("  " + binaryName + "\n")

	for _, section := range helpSections {
		b.WriteString("\n" + i18n.Translate(lang, section.titleKey) + "\n")
		for _, entry := range section.entries {
			b.WriteString(helpLine(entry.syntax, i18n.Translate(lang, entry.key)))
		}
	}

	b.WriteString("\n")
	b.WriteString(i18n.TranslateFormat(lang, "client.shells", map[string]string{
		"shells": strings.Join(supportedShells, ", "),
	}))
	b.WriteString("\n\n")
	b.WriteString(i18n.Translate(lang, "client.tui_hint"))
	b.WriteString("\n")
	b.WriteString(i18n.TranslateFormat(lang, "cli.run_help", map[string]string{"project_name": binaryName}))
	b.WriteString("\n")
	return b.String()
}

// helpLine renders one line, padding the syntax column so every description
// starts at the same offset.
func helpLine(syntax, description string) string {
	padding := helpSyntaxColumn - len([]rune(syntax))
	if padding < 1 {
		padding = 1
	}
	return syntax + strings.Repeat(" ", padding) + "- " + description + "\n"
}

// printHelp writes the client --help output.
func printHelp(w io.Writer, binaryName, lang string) {
	fmt.Fprint(w, helpText(binaryName, lang))
}

// printVersion writes the client --version output. AI.md PART 32 requires
// the same layout as the server binary, showing the ACTUAL binary name.
func printVersion(w io.Writer, binaryName, lang string) {
	fmt.Fprintln(w, i18n.TranslateFormat(lang, "version.name_version", map[string]string{
		"project_name":    binaryName,
		"project_version": version.Version,
	}))
	fmt.Fprintln(w, i18n.TranslateFormat(lang, "version.built", map[string]string{"build_date": version.BuildDate}))
	fmt.Fprintln(w, i18n.TranslateFormat(lang, "version.commit", map[string]string{"commit": version.CommitID}))
	fmt.Fprintln(w, i18n.TranslateFormat(lang, "version.go", map[string]string{"go_version": runtime.Version()}))
	fmt.Fprintln(w, i18n.TranslateFormat(lang, "version.os_arch", map[string]string{
		"goos":   runtime.GOOS,
		"goarch": runtime.GOARCH,
	}))
}
