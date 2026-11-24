// The `tor` subcommand: AI.md PART 31.1 "Tor CLI Commands" — status,
// validate, restart, regenerate, vanity start/stop/apply and import-keys.
// Changes are sent to the server-owned Tor manager through loopback control.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/shortner/src/common/pidfile"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/paths"
	"github.com/apimgr/shortner/src/tor"
)

// torHelp is the `tor --help` text, mirroring AI.md PART 31.1's command
// table.
const torHelp = `Tor hidden service management:

status                                - Show hidden service status and address
validate                              - Validate the Tor configuration
restart                               - Restart the hidden service
regenerate                            - Generate a brand new .onion address
                                        THE OLD ADDRESS IS LOST FOREVER

vanity start <prefix>                 - Search for an address starting with
                                        <prefix> (base32: a-z and 2-7 only)
vanity stop                           - Cancel the current search
vanity apply <address>                - Install a previously found address

import-keys <path>                    - Import an existing Tor key directory

Mutating commands require the server to be running.

Examples:
  %[1]s --maintenance tor status
  %[1]s --maintenance tor vanity start shrt
  %[1]s --maintenance tor import-keys /backup/onion-keys
`

// runTor dispatches `tor [COMMAND] [ARGS...]` and returns the process exit
// code: 0 on success, 1 on failure, 2 on a usage error.
func runTor(binaryName string, p paths.Paths, args []string) int {
	assumeYes := false
	filtered := args[:0]
	for _, arg := range args {
		if arg == "--yes" {
			assumeYes = true
		} else {
			filtered = append(filtered, arg)
		}
	}
	args = filtered
	command := ""
	rest := args
	if len(args) > 0 {
		command = args[0]
		rest = args[1:]
	}
	if command != "" && command != "help" && command != "--help" && command != "-h" {
		if err := authorizeMaintenance(binaryName, torOperatorToken(p)); err != nil {
			fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
			return 1
		}
	}

	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(torHelp, binaryName)
		return 0
	case "status":
		return runTorStatus(p)
	case "validate":
		return runTorValidate(p)
	case "restart":
		return runTorRestart(binaryName, p)
	case "regenerate":
		return runTorRegenerate(binaryName, p, assumeYes)
	case "vanity":
		return runTorVanity(binaryName, p, rest, assumeYes)
	case "import-keys":
		if len(rest) == 0 {
			fmt.Fprintf(os.Stderr, "%s: tor import-keys requires a path to hs_ed25519_secret_key\n", binaryName)
			return 2
		}
		return runTorImportKeys(binaryName, p, rest[0], assumeYes)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown tor command %q (run '%s tor --help')\n", binaryName, command, binaryName)
		return 2
	}
}

func torOperatorToken(p paths.Paths) string {
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return ""
	}
	return cfg.Server.Token
}

// torDirs builds the src/tor directory set from the resolved paths.
func torDirs(p paths.Paths) tor.Dirs {
	return tor.Dirs{Config: p.Config, Data: p.Data, Log: p.Logs}
}

// torCLIConfig loads server.yml and returns the Tor section. A missing or
// unreadable config falls back to the defaults so `tor status` still works
// before first run.
func torCLIConfig(p paths.Paths) config.Tor {
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return config.DefaultTor()
	}
	return cfg.Server.Tor
}

// serverRunning reports whether the server process holds the PID file.
func serverRunning(p paths.Paths) bool {
	running, _, err := pidfile.CheckPIDFile(p.PIDFile)
	return err == nil && running
}

// requireStopped enforces the stopped-server gate for the commands that
// rewrite the hidden service identity on disk.
func requireStopped(binaryName string, p paths.Paths, action string) bool {
	if !serverRunning(p) {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s: cannot %s while the server is running\n", binaryName, action)
	fmt.Fprintf(os.Stderr, "Stop it first: %s --service stop\n", binaryName)
	return false
}

// confirmOverlay asks a destructive-action question and reports whether the
// operator explicitly agreed. Anything other than yes cancels.
func confirmOverlay(prompt string) bool {
	answer, err := promptLine(prompt)
	if err != nil {
		return false
	}
	return strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
}

// runTorStatus prints the hidden service state from on-disk data, which is
// what an out-of-process command can observe without an IPC channel.
func runTorStatus(p paths.Paths) int {
	if serverRunning(p) {
		var data map[string]any
		if torControlRequest(p, http.MethodGet, "status", nil, &data) == nil {
			fmt.Println("Tor Hidden Service: Connected")
			if address, ok := data["onion_address"].(string); ok && address != "" {
				fmt.Printf("  Address: %s\n", address)
			}
			if vanity, ok := data["vanity"].(map[string]any); ok {
				if state, _ := vanity["state"].(string); state != "" && state != "idle" {
					fmt.Printf("  Vanity Search: %s\n", state)
				}
			}
			return 0
		}
	}
	cfg := torCLIConfig(p).Normalized()
	address := tor.ReadOnionAddress(p.Data)
	running := serverRunning(p)

	binary, binErr := tor.ResolveBinary(cfg)
	switch {
	case binErr != nil:
		fmt.Println("Tor Hidden Service: No Tor Binary")
	case address == "" && running:
		fmt.Println("Tor Hidden Service: Starting")
	case address == "":
		fmt.Println("Tor Hidden Service: Not Started")
	case running:
		fmt.Println("Tor Hidden Service: Connected")
	default:
		fmt.Println("Tor Hidden Service: Stopped")
	}
	if address != "" {
		fmt.Printf("  Address: %s\n", address)
		fmt.Printf("  Virtual Port: %d\n", cfg.VirtualPort)
	}
	if binErr == nil {
		fmt.Printf("  Binary: %s\n", binary)
	}
	fmt.Printf("  Torrc: %s\n", torDirs(p).TorrcPath())
	fmt.Printf("  Hidden Service Dir: %s\n", torDirs(p).SitePath())
	if staged := tor.ListVanity(p.Data); len(staged) > 0 {
		fmt.Printf("  Staged Vanity Addresses: %s\n", strings.Join(staged, ", "))
	}
	return 0
}

// runTorValidate checks the configuration the server would start Tor with:
// the binary resolves, the settings are in range, and the directories are
// creatable.
func runTorValidate(p paths.Paths) int {
	if serverRunning(p) {
		var data struct {
			Valid  bool `json:"valid"`
			Errors []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := torControlRequest(p, http.MethodPost, "validate", map[string]any{}, &data); err == nil {
			if data.Valid {
				fmt.Println("✓ server.tor settings are all in range")
				return 0
			}
			for _, e := range data.Errors {
				fmt.Printf("✗ %s: %s\n", e.Field, e.Message)
			}
			return 1
		}
	}
	raw := torCLIConfig(p)
	failed := false

	binary, err := tor.ResolveBinary(raw)
	if err != nil {
		fmt.Printf("✗ tor binary: %v\n", err)
		failed = true
	} else {
		fmt.Printf("✓ tor binary: %s\n", binary)
	}

	warnings := config.ValidateTor(raw)
	if len(warnings) == 0 {
		fmt.Println("✓ server.tor settings are all in range")
	}
	for _, w := range warnings {
		fmt.Printf("! %s\n", w)
	}

	if err := tor.EnsureDirs(torDirs(p)); err != nil {
		fmt.Printf("✗ directories: %v\n", err)
		failed = true
	} else {
		fmt.Printf("✓ directories: %s, %s\n", filepath.Join(p.Config, "tor"), torDirs(p).DataPath())
	}

	if failed {
		return 1
	}
	return 0
}

// runTorRestart reports how to restart the hidden service. Tor is a child
// of the server process and the server ignores SIGHUP by design (see
// src/signal), so there is no signal an external command could send to
// recycle it — restarting the server is the real answer.
func runTorRestart(binaryName string, p paths.Paths) int {
	if !serverRunning(p) {
		return noTorServer(binaryName)
	}
	var data map[string]any
	if err := torControlRequest(p, http.MethodPost, "restart", nil, &data); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Printf("Tor restarted: %v\n", data["onion_address"])
	return 0
}

// runTorRegenerate destroys the current identity so the next server start
// mints a new one. It confirms first, because the old address cannot be
// recovered.
func runTorRegenerate(binaryName string, p paths.Paths, yesOption ...bool) int {
	if !serverRunning(p) {
		return noTorServer(binaryName)
	}
	current := tor.ReadOnionAddress(p.Data)
	if current != "" {
		fmt.Printf("Current address: %s\n", current)
	}
	assumeYes := len(yesOption) > 0 && yesOption[0]
	if !assumeYes && !confirmOverlay("This permanently destroys the current .onion address. Continue? [y/N] ") {
		fmt.Println("Cancelled.")
		return 1
	}
	var data map[string]any
	if err := torControlRequest(p, http.MethodPost, "regenerate", map[string]any{"confirm": true}, &data); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Printf("New address: %v\n", data["onion_address"])
	return 0
}

// runTorVanity dispatches `tor vanity start|apply`.
func runTorVanity(binaryName string, p paths.Paths, args []string, yesOption ...bool) int {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	arg := ""
	if len(args) > 1 {
		arg = args[1]
	}
	switch sub {
	case "start":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "%s: tor vanity start requires a prefix\n", binaryName)
			return 2
		}
		workers := 0
		for i := 2; i < len(args); i++ {
			if args[i] != "--workers" || i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "%s: expected --workers <count>\n", binaryName)
				return 2
			}
			var err error
			workers, err = strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: workers must be an integer\n", binaryName)
				return 2
			}
			i++
		}
		return runTorVanityStart(binaryName, p, arg, workers)
	case "apply":
		if arg == "" {
			fmt.Fprintf(os.Stderr, "%s: tor vanity apply requires an address\n", binaryName)
			return 2
		}
		return runTorVanityApply(binaryName, p, arg, yesOption...)
	case "stop":
		if !serverRunning(p) {
			return noTorServer(binaryName)
		}
		var data map[string]any
		if err := torControlRequest(p, http.MethodPost, "vanity/stop", nil, &data); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
			return 1
		}
		fmt.Println("Vanity search stopped.")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown vanity command %q (expected start or apply)\n", binaryName, sub)
		return 2
	}
}

// runTorVanityStart searches for an address with the requested prefix and
// stages the result. The search runs happily while the server is up:
// nothing is installed until `vanity apply`.
func runTorVanityStart(binaryName string, p paths.Paths, prefix string, requestedWorkers ...int) int {
	if err := tor.ValidVanityPrefix(prefix); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 2
	}
	if !serverRunning(p) {
		return noTorServer(binaryName)
	}
	workers := 0
	if len(requestedWorkers) > 0 {
		workers = requestedWorkers[0]
	}
	var data map[string]any
	if err := torControlRequest(p, http.MethodPost, "vanity/start", map[string]any{"prefix": strings.ToLower(prefix), "workers": workers}, &data); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Printf("Vanity search started for %q in the server process.\n", strings.ToLower(prefix))
	return 0
}

// runTorVanityApply installs a staged vanity identity as the live hidden
// service identity.
func runTorVanityApply(binaryName string, p paths.Paths, address string, yesOption ...bool) int {
	if !serverRunning(p) {
		return noTorServer(binaryName)
	}
	assumeYes := len(yesOption) > 0 && yesOption[0]
	if !assumeYes && !confirmOverlay("Applying this address permanently replaces the current .onion address. Continue? [y/N] ") {
		fmt.Println("Cancelled.")
		return 1
	}
	var data map[string]any
	if err := torControlRequest(p, http.MethodPost, "vanity/apply", map[string]any{"address": address, "confirm": true}, &data); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Printf("Applied %v.\n", data["onion_address"])
	return 0
}

// runTorImportKeys installs an existing hs_ed25519_secret_key, validating
// it first so a malformed file never leaves Tor unable to start.
func runTorImportKeys(binaryName string, p paths.Paths, path string, yesOption ...bool) int {
	if !serverRunning(p) {
		return noTorServer(binaryName)
	}
	if current := tor.ReadOnionAddress(p.Data); current != "" {
		fmt.Printf("Current address: %s\n", current)
	}
	assumeYes := len(yesOption) > 0 && yesOption[0]
	if !assumeYes && !confirmOverlay("Importing Tor identity keys is permanent. Continue? [y/N] ") {
		fmt.Println("Cancelled.")
		return 1
	}
	var data map[string]any
	if err := torControlRequest(p, http.MethodPost, "import-keys", map[string]any{"path": path, "confirm": true}, &data); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	fmt.Printf("Keys imported: %v\n", data["onion_address"])
	return 0
}

func noTorServer(binaryName string) int {
	fmt.Fprintln(os.Stderr, "Error: no running server detected — start the server first")
	return 1
}

func torControlRequest(p paths.Paths, method, endpoint string, body any, result any) error {
	cfg, err := config.Load(p.ConfigFile, filepath.Join(p.DB, "server.db"))
	if err != nil {
		return err
	}
	port := strings.Split(cfg.Server.Port, ",")[0]
	if strings.TrimSpace(port) == "" || port == "0" {
		return fmt.Errorf("configured server port cannot be resolved")
	}
	address := strings.TrimSpace(cfg.Server.Listen)
	if address == "" || address == "0.0.0.0" || address == "::" {
		address = "127.0.0.1"
	}
	if strings.Contains(address, ":") && !strings.HasPrefix(address, "[") {
		address = "[" + address + "]"
	}
	var reader io.Reader
	if body != nil {
		data, e := json.Marshal(body)
		if e != nil {
			return e
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://"+address+":"+port+"/server/tor/"+endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("no running server detected — start the server first")
	}
	defer resp.Body.Close()
	var envelope struct {
		OK      bool            `json:"ok"`
		Data    json.RawMessage `json:"data"`
		Error   string          `json:"error"`
		Message string          `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.OK {
		if envelope.Message != "" {
			return fmt.Errorf("%s: %s", envelope.Error, envelope.Message)
		}
		return fmt.Errorf("tor control request failed with HTTP %d", resp.StatusCode)
	}
	if result != nil {
		return json.Unmarshal(envelope.Data, result)
	}
	return nil
}
