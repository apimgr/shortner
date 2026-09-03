package service

import (
	"reflect"
	"strings"
	"testing"
)

// TestEscalationChain checks each OS chain against AI.md PART 23
// "Escalation Detection by OS", order included — the order is the rule.
func TestEscalationChain(t *testing.T) {
	tests := []struct {
		goos string
		want []EscalationMethod
	}{
		{"linux", []EscalationMethod{MethodRoot, MethodSudo, MethodSu, MethodPkexec, MethodDoas}},
		{"darwin", []EscalationMethod{MethodRoot, MethodSudo, MethodOsascript}},
		{"windows", []EscalationMethod{MethodAdministrator, MethodUAC, MethodRunas}},
		{"freebsd", []EscalationMethod{MethodRoot, MethodDoas, MethodSudo, MethodSu}},
		{"openbsd", []EscalationMethod{MethodRoot, MethodDoas, MethodSudo, MethodSu}},
		{"netbsd", []EscalationMethod{MethodRoot, MethodDoas, MethodSudo, MethodSu}},
		{"dragonfly", []EscalationMethod{MethodRoot, MethodDoas, MethodSudo, MethodSu}},
	}
	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			if got := escalationChain(tc.goos); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("escalationChain(%s) = %v, want %v", tc.goos, got, tc.want)
			}
		})
	}
}

func TestNoEscalationErrorMessage(t *testing.T) {
	err := &NoEscalationError{Action: "Service management"}
	// AI.md PART 5 "Binary Implementation" item 6 wording.
	for _, want := range []string{
		"Service management requires administrator privileges.",
		"You do not have sudo/admin access on this system.",
		"Contact your system administrator to perform this action.",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want to contain %q", err.Error(), want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote([]string{"/usr/local/bin/shortner", "--service", "--install"})
	want := `'/usr/local/bin/shortner' '--service' '--install'`
	if got != want {
		t.Errorf("shellQuote = %q, want %q", got, want)
	}
	// A single quote inside an argument must not break out of the quoting.
	if got := shellQuote([]string{"a'b"}); got != `'a'\''b'` {
		t.Errorf("shellQuote(a'b) = %q", got)
	}
}

func TestAppleScriptString(t *testing.T) {
	if got, want := appleScriptString(`say "hi"`), `"say \"hi\""`; got != want {
		t.Errorf("appleScriptString = %q, want %q", got, want)
	}
	if got, want := appleScriptString(`back\slash`), `"back\\slash"`; got != want {
		t.Errorf("appleScriptString = %q, want %q", got, want)
	}
}

func TestElevationCommand(t *testing.T) {
	exe := "/usr/local/bin/shortner"
	args := []string{"--service", "--install"}

	tests := []struct {
		method   EscalationMethod
		wantPath string
		wantArg  string
	}{
		{MethodSudo, "sudo", "--install"},
		{MethodDoas, "doas", "--install"},
		{MethodPkexec, "pkexec", "--install"},
		{MethodSu, "su", "-c"},
		{MethodOsascript, "osascript", "-e"},
		{MethodRunas, "runas", "/user:Administrator"},
	}
	for _, tc := range tests {
		t.Run(string(tc.method), func(t *testing.T) {
			cmd, err := elevationCommand(tc.method, exe, args)
			if err != nil {
				t.Fatalf("elevationCommand: %v", err)
			}
			if !strings.Contains(cmd.Args[0], tc.wantPath) {
				t.Errorf("command = %q, want %q", cmd.Args[0], tc.wantPath)
			}
			joined := strings.Join(cmd.Args, " ")
			if !strings.Contains(joined, tc.wantArg) {
				t.Errorf("args = %q, want to contain %q", joined, tc.wantArg)
			}
			if !strings.Contains(joined, exe) {
				t.Errorf("args = %q, want to contain the binary path", joined)
			}
		})
	}

	// MethodRoot/MethodAdministrator are states, not commands.
	if _, err := elevationCommand(MethodRoot, exe, args); err == nil {
		t.Error("elevationCommand(MethodRoot) returned no error")
	}
}

// TestCanEscalateWhenElevated proves the PART 5 short-circuit: an already
// elevated process always reports that it can escalate, and the first
// available method is the "already root" state rather than a prompt.
func TestCanEscalateWhenElevated(t *testing.T) {
	if !IsElevated() {
		t.Skip("test process is not elevated")
	}
	if !CanEscalate() {
		t.Error("CanEscalate() = false while elevated")
	}
	methods := AvailableEscalationMethods()
	if len(methods) == 0 {
		t.Fatal("AvailableEscalationMethods() is empty while elevated")
	}
	if methods[0] != MethodRoot && methods[0] != MethodAdministrator {
		t.Errorf("first method = %v, want the already-root state", methods[0])
	}
}
