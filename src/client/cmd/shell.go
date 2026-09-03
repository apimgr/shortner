package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// shellFlags lists every client flag, used to generate completions.
var shellFlags = []string{
	"--help", "-h", "--version", "-v", "--shell", "--server", "--token",
	"--token-file", "--config", "--output", "--debug", "--color", "--lang",
	"--update", "--slug", "--url", "--expire", "--limit", "--page",
	"--force", "--quiet", "--verbose",
}

// shellCommands lists the client's positional commands for completion.
var shellCommands = []string{
	"shorten", "get", "list", "update", "delete", "stats", "health", "setup",
}

// shellHelp is the --shell help text.
const shellHelp = `Usage: %s --shell {completions,init,help} [SHELL]

Shell integration.

Commands:
  completions [SHELL]    Print shell completion script
  init [SHELL]           Print the snippet to evaluate for enabling
                         completions in the current shell session
  help                   Show this help

SHELL is one of: %s. Auto-detected from $SHELL if omitted.
`

// detectShell returns the basename of $SHELL, or "" if unset.
func detectShell() string {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		return ""
	}
	return filepath.Base(shellPath)
}

// runShell dispatches --shell {completions,init,help} [SHELL] and returns
// the process exit code.
func runShell(out, errOut io.Writer, binaryName, command, shellArg string) int {
	switch command {
	case "", "help", "--help", "-h":
		fmt.Fprintf(out, shellHelp, binaryName, strings.Join(supportedShells, ", "))
		return ExitOK
	case "completions":
		shellName, err := resolveShell(shellArg)
		if err != nil {
			fmt.Fprintln(errOut, binaryName+": "+err.Error())
			return ExitUsage
		}
		fmt.Fprint(out, completionScript(binaryName, shellName))
		return ExitOK
	case "init":
		shellName, err := resolveShell(shellArg)
		if err != nil {
			fmt.Fprintln(errOut, binaryName+": "+err.Error())
			return ExitUsage
		}
		fmt.Fprint(out, initSnippet(binaryName, shellName))
		return ExitOK
	default:
		fmt.Fprintf(errOut, "%s: unknown --shell command %q (run '%s --shell help')\n", binaryName, command, binaryName)
		return ExitUsage
	}
}

// resolveShell validates an explicit shell name or auto-detects one from
// $SHELL.
func resolveShell(shellArg string) (string, error) {
	name := shellArg
	if name == "" {
		name = detectShell()
	}
	if name == "" {
		return "", fmt.Errorf("could not detect shell from $SHELL; pass one explicitly (%s)", strings.Join(supportedShells, ", "))
	}
	for _, supported := range supportedShells {
		if name == supported {
			return name, nil
		}
	}
	return "", fmt.Errorf("unsupported shell %q (want %s)", name, strings.Join(supportedShells, ", "))
}

// initSnippet returns the line a user evaluates to load completions in the
// current session, per AI.md PART 32's printInit table.
func initSnippet(binaryName, shellName string) string {
	switch shellName {
	case "bash", "zsh":
		return fmt.Sprintf("source <(%s --shell completions %s)\n", binaryName, shellName)
	case "fish":
		return fmt.Sprintf("%s --shell completions fish | source\n", binaryName)
	case "powershell", "pwsh":
		return fmt.Sprintf("Invoke-Expression (& %s --shell completions powershell)\n", binaryName)
	default:
		return fmt.Sprintf("eval \"$(%s --shell completions %s)\"\n", binaryName, shellName)
	}
}

// completionScript returns the completion script for shellName.
func completionScript(binaryName, shellName string) string {
	switch shellName {
	case "bash":
		return bashCompletion(binaryName)
	case "zsh":
		return zshCompletion(binaryName)
	case "fish":
		return fishCompletion(binaryName)
	case "powershell", "pwsh":
		return powershellCompletion(binaryName)
	default:
		return posixCompletion(binaryName)
	}
}

// bashCompletion builds a bash completion script.
func bashCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s bash completion\n", binaryName)
	fmt.Fprintf(&b, "_%s_completions() {\n", sanitize(binaryName))
	b.WriteString("    local cur prev opts cmds\n")
	b.WriteString("    COMPREPLY=()\n")
	b.WriteString("    cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("    prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	fmt.Fprintf(&b, "    opts=\"%s\"\n", strings.Join(shellFlags, " "))
	fmt.Fprintf(&b, "    cmds=\"%s\"\n", strings.Join(shellCommands, " "))
	b.WriteString("    case \"$prev\" in\n")
	b.WriteString("        --shell) COMPREPLY=($(compgen -W \"completions init help\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --color) COMPREPLY=($(compgen -W \"auto yes no\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --output) COMPREPLY=($(compgen -W \"table json yaml plain csv\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --update) COMPREPLY=($(compgen -W \"check yes\" -- \"$cur\")); return ;;\n")
	b.WriteString("        --token-file) COMPREPLY=($(compgen -f -- \"$cur\")); return ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("    if [ \"$COMP_CWORD\" -eq 1 ]; then\n")
	b.WriteString("        COMPREPLY=($(compgen -W \"$cmds $opts\" -- \"$cur\"))\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n")
	b.WriteString("    COMPREPLY=($(compgen -W \"$opts\" -- \"$cur\"))\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "complete -F _%s_completions %s\n", sanitize(binaryName), binaryName)
	return b.String()
}

// zshCompletion builds a zsh completion script.
func zshCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#compdef %s\n", binaryName)
	fmt.Fprintf(&b, "# %s zsh completion\n", binaryName)
	fmt.Fprintf(&b, "_%s() {\n", sanitize(binaryName))
	b.WriteString("    local -a opts\n")
	b.WriteString("    opts=(\n")
	for _, flag := range shellFlags {
		fmt.Fprintf(&b, "        '%s'\n", flag)
	}
	b.WriteString("    )\n")
	b.WriteString("    case \"$words[CURRENT-1]\" in\n")
	b.WriteString("        --shell) _values 'shell command' completions init help; return ;;\n")
	b.WriteString("        --color) _values 'color' auto yes no; return ;;\n")
	b.WriteString("        --output) _values 'format' table json yaml plain csv; return ;;\n")
	b.WriteString("        --update) _values 'update command' check yes; return ;;\n")
	b.WriteString("        --token-file) _files; return ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("    if (( CURRENT == 2 )); then\n")
	fmt.Fprintf(&b, "        _values 'command' %s\n", strings.Join(shellCommands, " "))
	b.WriteString("        return\n")
	b.WriteString("    fi\n")
	b.WriteString("    _describe 'option' opts\n")
	b.WriteString("}\n")
	fmt.Fprintf(&b, "_%s \"$@\"\n", sanitize(binaryName))
	return b.String()
}

// fishCompletion builds a fish completion script.
func fishCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s fish completion\n", binaryName)
	flagLines := []struct{ flag, desc string }{
		{"help", "Show help"},
		{"version", "Show version"},
		{"shell", "Shell integration"},
		{"server", "Server URL"},
		{"token", "API token"},
		{"token-file", "Read token from file"},
		{"config", "Config profile name"},
		{"output", "Output format"},
		{"debug", "Enable debug mode"},
		{"color", "Color output"},
		{"lang", "Output language"},
		{"update", "Check or install a client update"},
		{"slug", "Custom slug"},
		{"url", "Destination URL"},
		{"expire", "Expiration timestamp"},
		{"limit", "Results per page"},
		{"page", "Page number"},
		{"force", "Skip confirmation"},
		{"quiet", "Suppress non-essential output"},
		{"verbose", "Verbose output"},
	}
	for _, line := range flagLines {
		fmt.Fprintf(&b, "complete -c %s -l %s -d '%s'\n", binaryName, line.flag, line.desc)
	}
	fmt.Fprintf(&b, "complete -c %s -s h -d 'Show help'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -s v -d 'Show version'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_use_subcommand' -a '%s'\n", binaryName, strings.Join(shellCommands, " "))
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l shell' -a 'completions init help'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l color' -a 'auto yes no'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l output' -a 'table json yaml plain csv'\n", binaryName)
	fmt.Fprintf(&b, "complete -c %s -n '__fish_seen_argument -l update' -a 'check yes'\n", binaryName)
	return b.String()
}

// posixCompletion builds a POSIX-shell completion script for sh, dash, and
// ksh, which share no common programmable-completion API — the script
// defines a helper that lists the available words.
func posixCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s POSIX shell completion helper\n", binaryName)
	fmt.Fprintf(&b, "%s_commands() {\n", sanitize(binaryName))
	fmt.Fprintf(&b, "    printf '%%s\\n' %s\n", strings.Join(shellCommands, " "))
	b.WriteString("}\n")
	fmt.Fprintf(&b, "%s_flags() {\n", sanitize(binaryName))
	fmt.Fprintf(&b, "    printf '%%s\\n' %s\n", strings.Join(shellFlags, " "))
	b.WriteString("}\n")
	fmt.Fprintf(&b, "if command -v complete >/dev/null 2>&1; then\n")
	fmt.Fprintf(&b, "    complete -W \"%s %s\" %s 2>/dev/null || true\n",
		strings.Join(shellCommands, " "), strings.Join(shellFlags, " "), binaryName)
	b.WriteString("fi\n")
	return b.String()
}

// powershellCompletion builds a PowerShell completion script.
func powershellCompletion(binaryName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s powershell completion\n", binaryName)
	fmt.Fprintf(&b, "Register-ArgumentCompleter -Native -CommandName %s -ScriptBlock {\n", binaryName)
	b.WriteString("    param($wordToComplete, $commandAst, $cursorPosition)\n")
	fmt.Fprintf(&b, "    $words = @(%s)\n", quoteList(append(append([]string{}, shellCommands...), shellFlags...)))
	b.WriteString("    $words | Where-Object { $_ -like \"$wordToComplete*\" } | ForEach-Object {\n")
	b.WriteString("        [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String()
}

// quoteList renders a PowerShell single-quoted array body.
func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = "'" + strings.ReplaceAll(item, "'", "''") + "'"
	}
	return strings.Join(quoted, ", ")
}

// sanitize turns a binary name into a valid shell identifier fragment.
func sanitize(binaryName string) string {
	return strings.ReplaceAll(binaryName, "-", "_")
}
