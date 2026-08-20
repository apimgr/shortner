package service

import (
	"runtime"
	"strings"
	"testing"
)

// takenSet builds an idLookup where the listed IDs are already in use.
func takenSet(ids ...int) idLookup {
	taken := map[int]bool{}
	for _, id := range ids {
		taken[id] = true
	}
	return idLookup{
		uidTaken: func(id int) bool { return taken[id] },
		gidTaken: func(id int) bool { return taken[id] },
	}
}

func TestSafeIDRange(t *testing.T) {
	// AI.md PART 23 "macOS UID/GID Ranges".
	if low, high := safeIDRange("darwin"); low != 200 || high != 399 {
		t.Errorf("safeIDRange(darwin) = %d-%d, want 200-399", low, high)
	}
	// AI.md PART 23 "UID/GID Selection Logic".
	for _, goos := range []string{"linux", "freebsd", "openbsd"} {
		if low, high := safeIDRange(goos); low != 200 || high != 899 {
			t.Errorf("safeIDRange(%s) = %d-%d, want 200-899", goos, low, high)
		}
	}
}

func TestFindAvailableSystemIDPicksTopOfRange(t *testing.T) {
	got, err := findAvailableSystemID("linux", takenSet())
	if err != nil {
		t.Fatalf("findAvailableSystemID: %v", err)
	}
	if got != 899 {
		t.Errorf("id = %d, want 899 (top of the safe range)", got)
	}
}

func TestFindAvailableSystemIDSkipsTakenIDs(t *testing.T) {
	got, err := findAvailableSystemID("linux", takenSet(899, 898, 897))
	if err != nil {
		t.Fatalf("findAvailableSystemID: %v", err)
	}
	if got != 896 {
		t.Errorf("id = %d, want 896", got)
	}
}

// TestFindAvailableSystemIDNeverReturnsReserved walks the whole safe
// range one free ID at a time and proves the result is never a reserved
// value and never leaves the range. The PART 23 safe range and the
// reserved list happen not to overlap today (see
// TestReservedIDsOutsideSafeRange), so the reserved check is
// defense-in-depth against a future range change — this test is what
// keeps it honest.
func TestFindAvailableSystemIDNeverReturnsReserved(t *testing.T) {
	low, high := safeIDRange("linux")
	for free := low; free <= high; free++ {
		lookup := idLookup{
			uidTaken: func(id int) bool { return id != free },
			gidTaken: func(id int) bool { return id != free },
		}
		got, err := findAvailableSystemID("linux", lookup)
		if reservedIDs[free] {
			if err == nil {
				t.Fatalf("free id %d is reserved but findAvailableSystemID returned %d", free, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("free id %d: %v", free, err)
		}
		if got != free {
			t.Fatalf("free id %d: got %d", free, got)
		}
		if got < low || got > high {
			t.Fatalf("id %d is outside the safe range %d-%d", got, low, high)
		}
	}
}

// TestReservedIDsOutsideSafeRange documents why the reserved list and
// the safe range coexist: every reserved ID sits below or above the
// 200-899 window PART 23 selects from.
func TestReservedIDsOutsideSafeRange(t *testing.T) {
	low, high := safeIDRange("linux")
	for id := range reservedIDs {
		if id >= low && id <= high {
			t.Errorf("reserved id %d falls inside the safe range %d-%d", id, low, high)
		}
	}
}

// TestFindAvailableSystemIDMatchingUIDGID proves the UID and the GID must
// both be free at the same number: a taken GID disqualifies the ID even
// when the UID is available.
func TestFindAvailableSystemIDMatchingUIDGID(t *testing.T) {
	lookup := idLookup{
		uidTaken: func(int) bool { return false },
		gidTaken: func(id int) bool { return id > 500 },
	}
	got, err := findAvailableSystemID("linux", lookup)
	if err != nil {
		t.Fatalf("findAvailableSystemID: %v", err)
	}
	if got != 500 {
		t.Errorf("id = %d, want 500", got)
	}
}

func TestFindAvailableSystemIDExhausted(t *testing.T) {
	lookup := idLookup{
		uidTaken: func(int) bool { return true },
		gidTaken: func(int) bool { return true },
	}
	if _, err := findAvailableSystemID("linux", lookup); err == nil {
		t.Fatal("expected an error when no ID is available")
	}
}

func TestFindAvailableSystemIDDarwinRange(t *testing.T) {
	got, err := findAvailableSystemID("darwin", takenSet())
	if err != nil {
		t.Fatalf("findAvailableSystemID: %v", err)
	}
	if got != 399 {
		t.Errorf("id = %d, want 399 (top of the macOS safe range)", got)
	}
}

// TestReservedIDsMatchSpec spot-checks the values transcribed from AI.md
// PART 23 "Go Implementation".
func TestReservedIDsMatchSpec(t *testing.T) {
	for _, id := range []int{65534, 999, 990, 980, 101, 110, 170, 179} {
		if !reservedIDs[id] {
			t.Errorf("reservedIDs is missing %d", id)
		}
	}
	for _, id := range []int{200, 500, 899, 100, 111, 169, 180} {
		if reservedIDs[id] {
			t.Errorf("reservedIDs wrongly contains %d", id)
		}
	}
}

func TestOwnsServiceAccount(t *testing.T) {
	p := Params{InternalName: "shortner", DataDir: t.TempDir()}
	if ownsServiceAccount(p) {
		t.Error("ownsServiceAccount = true with no marker file present")
	}

	if err := writeFile(accountMarker(p), "shortner\n", 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if !ownsServiceAccount(p) {
		t.Error("ownsServiceAccount = false after the marker was written")
	}

	if err := writeFile(accountMarker(p), "someone-else\n", 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if ownsServiceAccount(p) {
		t.Error("ownsServiceAccount = true for a marker naming a different account")
	}
}

func TestNologinShell(t *testing.T) {
	// The chosen shell is host dependent, but it must always be one of
	// the PART 23 no-login shells.
	got := nologinShell()
	known := map[string]bool{
		"/sbin/nologin": true, "/usr/sbin/nologin": true,
		"/bin/false": true, "/usr/bin/false": true,
	}
	if !known[got] {
		t.Errorf("nologinShell() = %q, not a known no-login shell", got)
	}
}

// TestCreateServiceAccountLinux drives the PART 23 account creation with
// the external commands stubbed out — creating a real system user is not
// something a test may do, so the assertion is on the exact command
// lines and on the ownership marker.
func TestCreateServiceAccountLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the groupadd/useradd path is Linux only")
	}
	if AccountExists("shortner-test-account") {
		t.Skip("a real account with the test name exists on this host")
	}

	root := t.TempDir()
	p := Params{
		InternalName: "shortner-test-account",
		ConfigDir:    root + "/config",
		DataDir:      root + "/data",
	}

	var commands []string
	original := run
	t.Cleanup(func() { run = original })
	run = func(name string, args ...string) error {
		commands = append(commands, name+" "+strings.Join(args, " "))
		return nil
	}

	id, err := CreateServiceAccount(p)
	if err != nil {
		t.Fatalf("CreateServiceAccount: %v", err)
	}
	low, high := safeIDRange(runtime.GOOS)
	if id < low || id > high {
		t.Errorf("uid = %d, outside the safe range %d-%d", id, low, high)
	}
	if reservedIDs[id] {
		t.Errorf("uid = %d, which is reserved", id)
	}

	joined := strings.Join(commands, "\n")
	for _, want := range []string{
		"groupadd --system --gid",
		"useradd --system --uid",
		"--home-dir " + p.ConfigDir,
		"--shell ",
		p.InternalName,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("commands %q missing %q", joined, want)
		}
	}
	// The home directory must exist before useradd runs.
	if !fileExists(p.ConfigDir) {
		t.Error("the home directory was not created")
	}
	if !ownsServiceAccount(p) {
		t.Error("the ownership marker was not written")
	}
}

// TestDeleteServiceAccountMissing proves uninstall is a no-op for an
// account that is not there.
func TestDeleteServiceAccountMissing(t *testing.T) {
	var called bool
	original := run
	t.Cleanup(func() { run = original })
	run = func(string, ...string) error {
		called = true
		return nil
	}
	if err := DeleteServiceAccount("shortner-absent-account"); err != nil {
		t.Fatalf("DeleteServiceAccount: %v", err)
	}
	if called {
		t.Error("a deletion command ran for a non-existent account")
	}
}
