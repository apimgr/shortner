// Privilege escalation detection and re-execution. See AI.md PART 23
// "Escalation Detection by OS" for the per-OS ordered method chains, and
// PART 5 "Privileged Port Binding (<1024)" → "Binary Implementation"
// items 3 and 6 for the smart escalation flow and its error text.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/apimgr/shortner/src/paths"
)

// EscalationMethod is one way to gain administrator privileges.
type EscalationMethod string

// The escalation methods named in PART 23 "Escalation Detection by OS".
const (
	// MethodRoot means the process already holds root/Administrator.
	MethodRoot EscalationMethod = "root"
	// MethodAdministrator is the Windows spelling of MethodRoot.
	MethodAdministrator EscalationMethod = "administrator"
	MethodSudo          EscalationMethod = "sudo"
	MethodSu            EscalationMethod = "su"
	MethodPkexec        EscalationMethod = "pkexec"
	MethodDoas          EscalationMethod = "doas"
	MethodOsascript     EscalationMethod = "osascript"
	MethodUAC           EscalationMethod = "uac"
	MethodRunas         EscalationMethod = "runas"
)

// escalationChain returns the ordered escalation methods for an OS,
// transcribed from PART 23 "Escalation Detection by OS".
func escalationChain(goos string) []EscalationMethod {
	switch goos {
	case "darwin":
		return []EscalationMethod{MethodRoot, MethodSudo, MethodOsascript}
	case "windows":
		return []EscalationMethod{MethodAdministrator, MethodUAC, MethodRunas}
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return []EscalationMethod{MethodRoot, MethodDoas, MethodSudo, MethodSu}
	default:
		return []EscalationMethod{MethodRoot, MethodSudo, MethodSu, MethodPkexec, MethodDoas}
	}
}

// IsElevated reports whether the process already has root (Unix) or an
// Administrator token (Windows).
func IsElevated() bool {
	if runtime.GOOS == "windows" {
		return paths.IsAdministrator()
	}
	return paths.IsPrivileged()
}

// methodAvailable reports whether a single escalation method can actually
// be used on this machine right now.
func methodAvailable(method EscalationMethod) bool {
	switch method {
	case MethodRoot, MethodAdministrator:
		return IsElevated()
	case MethodSudo:
		return hasBinary("sudo") && (sudoNonInteractiveWorks() || inAdminLikeGroup())
	case MethodSu:
		return hasBinary("su")
	case MethodPkexec:
		return hasBinary("pkexec")
	case MethodDoas:
		// "if configured" — doas refuses to do anything without a rules
		// file, so an unconfigured doas is not an escalation path.
		return hasBinary("doas") && (fileExists("/etc/doas.conf") || fileExists("/usr/local/etc/doas.conf"))
	case MethodOsascript:
		// The GUI authorization prompt needs a window server session.
		return hasBinary("osascript") && os.Getenv("SSH_CONNECTION") == ""
	case MethodUAC:
		return inAdminLikeGroup()
	case MethodRunas:
		return hasBinary("runas") && inAdminLikeGroup()
	default:
		return false
	}
}

// AvailableEscalationMethods returns, in the PART 23 order for this OS,
// every escalation method that is actually usable here.
func AvailableEscalationMethods() []EscalationMethod {
	var available []EscalationMethod
	for _, method := range escalationChain(runtime.GOOS) {
		if methodAvailable(method) {
			available = append(available, method)
		}
	}
	return available
}

// CanEscalate reports whether the calling user can reach administrator
// privileges by any of this OS's methods.
func CanEscalate() bool {
	return len(AvailableEscalationMethods()) > 0
}

// hasBinary reports whether a command is on PATH.
func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// sudoNonInteractiveWorks reports whether the user has passwordless sudo
// (PART 5 "canEscalate", the `sudo -n true` probe).
var sudoNonInteractiveWorks = func() bool {
	return exec.Command("sudo", "-n", "true").Run() == nil
}

// inAdminLikeGroup reports whether the current user belongs to a group
// that grants administrator access (PART 5 "canEscalate"). On Windows the
// check is delegated to the Administrators-group token lookup.
var inAdminLikeGroup = func() bool {
	if runtime.GOOS == "windows" {
		return inWindowsAdminGroup()
	}
	u, err := user.Current()
	if err != nil {
		return false
	}
	gids, err := u.GroupIds()
	if err != nil {
		return false
	}
	for _, gid := range gids {
		group, err := user.LookupGroupId(gid)
		if err != nil || group == nil {
			continue
		}
		switch group.Name {
		case "sudo", "wheel", "admin", "root":
			return true
		}
	}
	return false
}

// ErrEscalationDeclined is returned when the operator answers no to the
// escalation prompt.
var ErrEscalationDeclined = fmt.Errorf("escalation declined")

// NoEscalationError is the PART 5 item 6 message shown when the user
// simply cannot escalate — the binary informs instead of prompting.
type NoEscalationError struct {
	Action string
}

func (e *NoEscalationError) Error() string {
	return fmt.Sprintf("%s requires administrator privileges.\n\n"+
		"You do not have sudo/admin access on this system.\n"+
		"Contact your system administrator to perform this action.", e.Action)
}

// ExecElevated re-executes this binary with the given arguments under the
// first available escalation method, streaming the child's I/O to the
// caller's terminal. args are the arguments after the program name.
func ExecElevated(args []string) error {
	methods := AvailableEscalationMethods()
	if len(methods) == 0 {
		return &NoEscalationError{Action: "This command"}
	}
	if methods[0] == MethodRoot || methods[0] == MethodAdministrator {
		// Already elevated — nothing to re-exec.
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("service: resolve executable path: %w", err)
	}

	var lastErr error
	for _, method := range methods {
		cmd, buildErr := elevationCommand(method, exe, args)
		if buildErr != nil {
			lastErr = buildErr
			continue
		}
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("service: no escalation method succeeded")
	}
	return lastErr
}

// elevationCommand builds the command line for one escalation method.
func elevationCommand(method EscalationMethod, exe string, args []string) (*exec.Cmd, error) {
	switch method {
	case MethodSudo:
		return exec.Command("sudo", append([]string{exe}, args...)...), nil
	case MethodDoas:
		return exec.Command("doas", append([]string{exe}, args...)...), nil
	case MethodPkexec:
		return exec.Command("pkexec", append([]string{exe}, args...)...), nil
	case MethodSu:
		return exec.Command("su", "-c", shellQuote(append([]string{exe}, args...))), nil
	case MethodOsascript:
		script := fmt.Sprintf("do shell script %s with administrator privileges",
			appleScriptString(shellQuote(append([]string{exe}, args...))))
		return exec.Command("osascript", "-e", script), nil
	case MethodRunas, MethodUAC:
		return exec.Command("runas", "/user:Administrator", shellQuote(append([]string{exe}, args...))), nil
	default:
		return nil, fmt.Errorf("service: %s is not a re-executable escalation method", method)
	}
}

// shellQuote renders an argv as a single single-quoted shell command line.
func shellQuote(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return strings.Join(quoted, " ")
}

// appleScriptString renders a Go string as an AppleScript string literal.
func appleScriptString(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}
