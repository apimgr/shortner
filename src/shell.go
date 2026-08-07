// --shell command handling: completions, init snippet, and help. See
// AI.md PART 8 "Server Binary Commands".
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// shellFlags lists every server-binary flag, used to generate completions.
var shellFlags = []string{
	"--help", "-h", "--version", "-v", "--shell", "--mode", "--config",
	"--data", "--cache", "--log", "--backup", "--pid", "--address",
	"--port", "--baseurl", "--daemon", "--debug", "--color", "--lang",
	"--status", "--service", "--maintenance", "--update",
}

// shellHelp is the --shell --help text.
const shellHelp = `Usage: %s --shell {completions,init,help} [SHELL]

Shell integration.

Commands:
  completions [SHELL]    Print shell completion script
  init [SHELL]            Print the snippet to 'eval' for enabling
                           completions in the current shell session
  help                     Show this help

SHELL is one of: bash, zsh, fish. Auto-detected from $SHELL if omitted.
`

// detectShell returns the basename of $SHELL, or "" if unset/unknown.
func detectShell() string {
	return filepath.Base(os.Getenv("SHELL"))
}

// runShell dispatches --shell {completions,init,help} [SHELL] and returns
// the process exit code.
func runShell(binaryName, command, shellArg string) int {
	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(shellHelp, binaryName)
		return 0
	case "completions", "init":
		shellName := shellArg
		if shellName == "" {
			shellName = detectShell()
		}
		script, err := completionScript(binaryName, shellName)
		if err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
		fmt.Print(script)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown --shell command %q (run '%s --shell help')\n", binaryName, command, binaryName)
		return 1
	}
}

// completionScript returns the completion script text for shellName. init
// and completions print identical content — init's snippet IS the
// completion script, meant for 'eval "$(%s --shell init bash)"'.
func completionScript(binaryName, shellName string) (string, error) {
	switch shellName {
	case "bash":
		return bashCompletion(binaryName), nil
	case "zsh":
		return zshCompletion(binaryName), nil
	case "fish":
		return fishCompletion(binaryName), nil
	case "":
		return "", fmt.Errorf("could not detect shell from $SHELL; pass one explicitly (bash, zsh, fish)")
	default:
		return "", fmt.Errorf("unsupported shell %q (want bash, zsh, or fish)", shellName)
	}
}

func bashCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s bash completion\n", binaryName)
	fmt.Fprintf(&b, "_%s_completions() {\n", sanitize(binaryName))
	b.WriteString("    local cur prev opts\n")
	b.WriteString("    COMPREPLY=()\n")
	b.WriteString("    cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("    prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	fmt.Fprintf(&b, "    opts=\"%s\"\n", strings.Join(shellFlags, " "))
	b.WriteString("    case \"$prev\" in\n")
	b.WriteString("        --shell) COMPREPLY=($(compgen -W \"completions init help\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --mode) COMPREPLY=($(compgen -W \"production development\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --color) COMPREPLY=($(compgen -W \"auto yes no\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --service) COMPREPLY=($(compgen -W \"start restart stop reload --install --uninstall --disable --help\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --maintenance) COMPREPLY=($(compgen -W \"backup restore update mode setup pgp secret token data compliance --help\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --update) COMPREPLY=($(compgen -W \"check yes branch --help\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --config|--data|--cache|--log|--backup|--pid)\n")
	b.WriteString("            COMPREPLY=($(compgen -d -- \"$cur\")); return ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("    COMPREPLY=($(compgen -W \"$opts\" -- \"$cur\"))\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "complete -F _%s_completions %s\n", sanitize(binaryName), binaryName)
	return b.String()
}

func zshCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#compdef %s\n", binaryName)
	fmt.Fprintf(&b, "# %s zsh completion\n", binaryName)
	fmt.Fprintf(&b, "_%s() {\n", sanitize(binaryName))
	b.WriteString("    local -a opts\n")
	b.WriteString("    opts=(\n")
	for _, f := range shellFlags {
		fmt.Fprintf(&b, "        '%s'\n", f)
	}
	b.WriteString("    )\n")
	b.WriteString("    case \"$words[CURRENT-1]\" in\n")
	b.WriteString("        --shell) _values 'shell command' completions init help; return ;;\n")
	b.WriteString("        --mode) _values 'mode' production development; return ;;\n")
	b.WriteString("        --color) _values 'color' auto yes no; return ;;\n")
	b.WriteString("        --service) _values 'service command' start restart stop reload --install --uninstall --disable --help; return ;;\n")
	b.WriteString("        --maintenance) _values 'maintenance command' backup restore update mode setup pgp secret token data compliance --help; return ;;\n")
	b.WriteString("        --update) _values 'update command' check yes branch --help; return ;;\n")
	b.WriteString("        --config|--data|--cache|--log|--backup|--pid) _path_files -/; return ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("    _describe 'option' opts\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "_%s \"$@\"\n", sanitize(binaryName))
	return b.String()
}

func fishCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s fish completion\n", binaryName)
	flagLines := []struct{ flag, desc string }{
		{"help", "Show help"},
		{"version", "Show version"},
		{"shell", "Shell integration"},
		{"mode", "Application mode"},
		{"config", "Config directory"},
		{"data", "Data directory"},
		{"cache", "Cache directory"},
		{"log", "Log directory"},
		{"backup", "Backup directory"},
		{"pid", "PID file path"},
		{"address", "Listen address"},
		{"port", "Listen port"},
		{"baseurl", "URL path prefix"},
		{"daemon", "Run as daemon"},
		{"debug", "Enable debug mode"},
		{"color", "Color output"},
		{"lang", "Output language"},
		{"status", "Show server status"},
		{"service", "Service management"},
		{"maintenance", "Maintenance operations"},
		{"update", "Check/perform updates"},
	}
	for _, fl := range flagLines {
		fmt.Fprintf(&b, "complete -c %s -l %s -d '%s'\n", binaryName, fl.flag, fl.desc)
	}
	fmt.Fprintf(&b, "complete -c %s -s h -d 'Show help'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -s v -d 'Show version'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l shell' -a 'completions init help'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l mode' -a 'production development'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l color' -a 'auto yes no'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l service' -a 'start restart stop reload --install --uninstall --disable --help'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l maintenance' -a 'backup restore update mode setup pgp secret token data compliance --help'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l update' -a 'check yes branch --help'\n", binaryName)
	return b.String()
}

// sanitize turns a binary name into a valid shell identifier fragment
// (dashes are not valid in bash/zsh function names).
func sanitize(binaryName string) string {
	return strings.ReplaceAll(binaryName, "-", "_")
}
