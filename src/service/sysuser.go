// System service account management. See AI.md PART 23 "System User
// Requirements", "UID/GID Selection Logic", "Reserved/Well-Known UIDs",
// "Platform-Specific Commands", "macOS Service Account", and "Windows
// Service Account".
//
// Default rule (PART 23 "Platform-Specific Commands"): always create and
// use a dedicated service user/group. The documented exception — a
// project explicitly approved in IDEA.md to run permanently as
// root/Administrator — does not apply here: this project's IDEA.md
// contains no such approval, so the dedicated-account default stands.
package service

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// reservedIDs lists UIDs/GIDs used by well-known services across distros.
// NEVER use these even if they appear available on the current system.
// Transcribed from AI.md PART 23 "Go Implementation".
var reservedIDs = map[int]bool{
	// nobody
	65534: true,
	// systemd-*, docker
	999: true, 998: true, 997: true, 996: true, 995: true,
	// systemd-*, kvm
	994: true, 993: true, 992: true, 991: true, 990: true,
	// sgx, pipewire, colord
	989: true, 988: true, 987: true, 986: true, 985: true,
	// avahi, rtkit, saned
	984: true, 983: true, 982: true, 981: true, 980: true,
	// Database and common services (101-110, 170-179)
	101: true, 102: true, 103: true, 104: true, 105: true,
	106: true, 107: true, 108: true, 109: true, 110: true,
	170: true, 171: true, 172: true, 173: true, 174: true,
	175: true, 176: true, 177: true, 178: true, 179: true,
}

// safeIDRange returns the inclusive [low, high] system ID range for an
// OS: 200-899 everywhere (PART 23 "UID/GID Selection Logic"), narrowed to
// 200-399 on macOS (PART 23 "macOS UID/GID Ranges").
func safeIDRange(goos string) (low, high int) {
	if goos == "darwin" {
		return 200, 399
	}
	return 200, 899
}

// idLookup abstracts the passwd/group lookups so the selection logic is
// testable without touching the host's user database.
type idLookup struct {
	uidTaken func(int) bool
	gidTaken func(int) bool
}

// hostIDLookup queries the real system databases.
func hostIDLookup() idLookup {
	return idLookup{
		uidTaken: func(id int) bool {
			_, err := user.LookupId(strconv.Itoa(id))
			return err == nil
		},
		gidTaken: func(id int) bool {
			_, err := user.LookupGroupId(strconv.Itoa(id))
			return err == nil
		},
	}
}

// findAvailableSystemID implements PART 23's selection logic: walk down
// from the top of the OS's safe range, skip reserved IDs, and return the
// first value where BOTH the UID and the GID are free — the UID and GID
// must be the same number.
func findAvailableSystemID(goos string, lookup idLookup) (int, error) {
	low, high := safeIDRange(goos)
	for id := high; id >= low; id-- {
		if reservedIDs[id] {
			continue
		}
		if lookup.uidTaken(id) {
			continue
		}
		if lookup.gidTaken(id) {
			continue
		}
		return id, nil
	}
	return 0, fmt.Errorf("service: no available UID/GID in safe range %d-%d", low, high)
}

// FindAvailableSystemID returns a free matching UID/GID on this host.
func FindAvailableSystemID() (int, error) {
	return findAvailableSystemID(runtime.GOOS, hostIDLookup())
}

// nologinShell returns the first no-login shell that exists on this host,
// per PART 23 "System User Requirements" (/sbin/nologin or
// /usr/sbin/nologin).
func nologinShell() string {
	for _, shell := range []string{"/sbin/nologin", "/usr/sbin/nologin", "/bin/false", "/usr/bin/false"} {
		if fileExists(shell) {
			return shell
		}
	}
	return "/sbin/nologin"
}

// AccountExists reports whether the service account is already present.
func AccountExists(name string) bool {
	_, err := user.Lookup(name)
	return err == nil
}

// accountMarker is the file recording that this binary created the
// service account, so --service --uninstall only deletes a user it owns
// (PART 23 "Service Uninstall Logic" step 5, "if created by server").
func accountMarker(p Params) string {
	return filepath.Join(p.DataDir, ".service-account")
}

// ownsServiceAccount reports whether the marker records this binary as
// the creator of the service account named in Params.
func ownsServiceAccount(p Params) bool {
	if p.DataDir == "" {
		return false
	}
	data, err := os.ReadFile(accountMarker(p))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == p.InternalName
}

// CreateServiceAccount creates the dedicated {internal_name} system
// user and group with matching UID/GID, a no-login shell, no password,
// and the config directory as its home. It is idempotent: an existing
// account is reused and its UID returned.
//
// Windows needs no account at all — the service runs as its Virtual
// Service Account (NT SERVICE\{internal_name}), created and managed by
// Windows when the service is installed.
func CreateServiceAccount(p Params) (int, error) {
	if runtime.GOOS == "windows" {
		return 0, nil
	}
	if u, err := user.Lookup(p.InternalName); err == nil {
		id, convErr := strconv.Atoi(u.Uid)
		if convErr != nil {
			return 0, fmt.Errorf("service: existing account %s has a non-numeric uid %q", p.InternalName, u.Uid)
		}
		return id, nil
	}

	id, err := FindAvailableSystemID()
	if err != nil {
		return 0, err
	}

	// PART 23 "Home Directory Selection": the home directory must exist
	// before user creation.
	home := p.ConfigDir
	if home == "" {
		home = p.DataDir
	}
	if home != "" {
		if err := os.MkdirAll(home, 0o755); err != nil {
			return 0, fmt.Errorf("service: create home directory %s: %w", home, err)
		}
	}

	if err := createAccount(p, id, home); err != nil {
		return 0, err
	}
	if p.DataDir != "" {
		if err := os.MkdirAll(p.DataDir, 0o755); err == nil {
			// The marker is advisory; failing to write it only means a
			// later uninstall will leave the account in place.
			_ = os.WriteFile(accountMarker(p), []byte(p.InternalName+"\n"), 0o644)
		}
	}
	return id, nil
}

// createAccount runs the per-OS account creation commands from PART 23
// "Platform-Specific Commands", "macOS Service Account", and the FreeBSD
// block.
func createAccount(p Params, id int, home string) error {
	name := p.InternalName
	gecos := name + " service account"

	switch runtime.GOOS {
	case "darwin":
		return createDarwinAccount(name, id, home)
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		if err := run("pw", "groupadd", "-n", name, "-g", strconv.Itoa(id)); err != nil {
			return err
		}
		return run("pw", "useradd", "-n", name, "-u", strconv.Itoa(id), "-g", strconv.Itoa(id),
			"-d", home, "-s", nologinShell(), "-c", gecos)
	default:
		if err := run("groupadd", "--system", "--gid", strconv.Itoa(id), name); err != nil {
			return err
		}
		return run("useradd", "--system", "--uid", strconv.Itoa(id), "--gid", strconv.Itoa(id),
			"--home-dir", home, "--shell", nologinShell(), "--comment", gecos, name)
	}
}

// createDarwinAccount runs the dscl sequence from PART 23 "macOS Service
// Account" verbatim, including IsHidden so the account never appears on
// the login window.
func createDarwinAccount(name string, id int, home string) error {
	idStr := strconv.Itoa(id)
	commands := [][]string{
		// Create group
		{"dscl", ".", "-create", "/Groups/" + name},
		{"dscl", ".", "-create", "/Groups/" + name, "PrimaryGroupID", idStr},
		{"dscl", ".", "-create", "/Groups/" + name, "Password", "*"},
		// Create user
		{"dscl", ".", "-create", "/Users/" + name},
		{"dscl", ".", "-create", "/Users/" + name, "UniqueID", idStr},
		{"dscl", ".", "-create", "/Users/" + name, "PrimaryGroupID", idStr},
		{"dscl", ".", "-create", "/Users/" + name, "UserShell", "/usr/bin/false"},
		{"dscl", ".", "-create", "/Users/" + name, "RealName", name + " service account"},
		{"dscl", ".", "-create", "/Users/" + name, "NFSHomeDirectory", home},
		{"dscl", ".", "-create", "/Users/" + name, "Password", "*"},
		{"dscl", ".", "-create", "/Users/" + name, "IsHidden", "1"},
	}
	for _, cmd := range commands {
		if err := run(cmd[0], cmd[1:]...); err != nil {
			return err
		}
	}
	return nil
}

// DeleteServiceAccount removes the dedicated user and group created by
// CreateServiceAccount (PART 23 "Service Uninstall Logic" step 5).
// Windows Virtual Service Accounts are removed by Windows together with
// the service, so there is nothing to delete there.
func DeleteServiceAccount(name string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if !AccountExists(name) {
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		if err := run("dscl", ".", "-delete", "/Users/"+name); err != nil {
			return err
		}
		// The group may already be gone; that is a success for uninstall.
		_ = run("dscl", ".", "-delete", "/Groups/"+name)
		return nil
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		if err := run("pw", "userdel", "-n", name); err != nil {
			return err
		}
		_ = run("pw", "groupdel", "-n", name)
		return nil
	default:
		if err := run("userdel", name); err != nil {
			return err
		}
		_ = run("groupdel", name)
		return nil
	}
}
