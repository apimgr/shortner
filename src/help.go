// --help and --version output. See AI.md PART 8 "Server --help Output".
// Every description is looked up through the PART 30 translation catalog,
// so --lang / server.i18n.default_language / LANG change the help text
// while the flag syntax itself stays untranslated (a flag name is part of
// the command-line contract, not prose).
package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/apimgr/shortner/src/common/version"
)

// projectDescription is the one-line project description shown in --help,
// from IDEA.md "## Project description".
const projectDescription = "A self-hosted URL shortening service with an API and web interface"

// helpSyntaxColumn is the width of the flag-syntax column; descriptions
// start after it, separated by "- ", matching AI.md PART 8's layout.
const helpSyntaxColumn = 39

// helpEntry is one line of --help: the literal flag syntax plus the
// translation key holding its description.
type helpEntry struct {
	syntax string
	key    string
}

// helpSection is one titled block of --help.
type helpSection struct {
	titleKey string
	entries  []helpEntry
}

// helpSections mirrors AI.md PART 8 "Server --help Output" section for
// section and flag for flag.
var helpSections = []helpSection{
	{
		titleKey: "cli.information",
		entries: []helpEntry{
			{"-h, --help", "cli.flag_help"},
			{"-v, --version", "cli.flag_version"},
			{"--status", "cli.flag_status"},
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
		titleKey: "cli.server_configuration",
		entries: []helpEntry{
			{"--mode {production|development|debug}", "cli.flag_mode"},
			{"--config DIR", "cli.flag_config"},
			{"--data DIR", "cli.flag_data"},
			{"--cache DIR", "cli.flag_cache"},
			{"--log DIR", "cli.flag_log"},
			{"--backup DIR", "cli.flag_backup"},
			{"--pid FILE", "cli.flag_pid"},
			{"--address ADDR", "cli.flag_address"},
			{"--port PORT", "cli.flag_port"},
			{"--baseurl PATH", "cli.flag_baseurl"},
			{"--daemon", "cli.flag_daemon"},
			{"--debug", "cli.flag_debug"},
			{"--color {auto|yes|no}", "cli.flag_color"},
			{"--lang CODE", "cli.flag_lang"},
		},
	},
	{
		titleKey: "cli.service_management",
		entries: []helpEntry{
			{"--service CMD", "cli.flag_service"},
			{"--maintenance CMD", "cli.flag_maintenance"},
			{"--update [CMD]", "cli.flag_update"},
			{"email [CMD]", "cli.cmd_email"},
			{"tor [CMD]", "cli.cmd_tor"},
			{"i2p [CMD]", "cli.cmd_i2p"},
			{"--include-ssl", "cli.flag_include_ssl"},
			{"--include-data", "cli.flag_include_data"},
		},
	},
}

// helpText builds the full --help output for binaryName in the process
// output language.
func helpText(binaryName string) string {
	var b strings.Builder

	b.WriteString(cliTF("cli.description", map[string]string{
		"project_name":        binaryName,
		"project_version":     version.Version,
		"project_description": projectDescription,
	}))
	b.WriteString("\n\n")

	b.WriteString(cliT("cli.usage"))
	b.WriteString("\n  " + binaryName + " [flags]\n")

	for _, section := range helpSections {
		b.WriteString("\n" + cliT(section.titleKey) + "\n")
		for _, entry := range section.entries {
			b.WriteString(helpLine(entry.syntax, cliT(entry.key)))
		}
	}

	b.WriteString("\n")
	b.WriteString(cliTF("cli.run_help", map[string]string{"project_name": binaryName}))
	b.WriteString("\n")
	return b.String()
}

// helpLine renders one flag line, padding the syntax column so every
// description starts at the same offset. A syntax string longer than the
// column still gets a single separating space rather than being truncated.
func helpLine(syntax, description string) string {
	padding := helpSyntaxColumn - len([]rune(syntax))
	if padding < 1 {
		padding = 1
	}
	return syntax + strings.Repeat(" ", padding) + "- " + description + "\n"
}

// printHelp prints the server --help output for binaryName.
func printHelp(binaryName string) {
	fmt.Print(helpText(binaryName))
}

// printVersion prints the server --version output for binaryName, in the
// layout of AI.md PART 8 "--version Output" plus the commit hash PART 11
// lists as public build metadata.
func printVersion(binaryName string) {
	fmt.Println(cliTF("version.name_version", map[string]string{
		"project_name":    binaryName,
		"project_version": version.Version,
	}))
	fmt.Println(cliTF("version.built", map[string]string{"build_date": version.BuildDate}))
	fmt.Println(cliTF("version.commit", map[string]string{"commit": version.CommitID}))
	fmt.Println(cliTF("version.go", map[string]string{"go_version": runtime.Version()}))
	fmt.Println(cliTF("version.os_arch", map[string]string{
		"goos":   runtime.GOOS,
		"goarch": runtime.GOARCH,
	}))
}
