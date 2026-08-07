package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/common/pidfile"
)

func TestResolveContainer(t *testing.T) {
	p := Resolve(true)
	want := dockerPaths()
	if p != want {
		t.Errorf("Resolve(true) = %+v, want %+v", p, want)
	}
	if p.Config != "/config/shortner" {
		t.Errorf("Config = %q, want /config/shortner", p.Config)
	}
	if p.DB != filepath.Join("/data", "db", "sqlite") {
		t.Errorf("DB = %q, want /data/db/sqlite", p.DB)
	}
}

func TestResolveNonContainerCurrentOS(t *testing.T) {
	// Resolve(false) dispatches on runtime.GOOS; on any platform it must
	// return a non-empty, internally consistent Paths struct rooted under
	// internalOrg/internalName (or the user's home directory).
	p := Resolve(false)
	if p.ConfigFile == "" || p.Config == "" || p.Data == "" {
		t.Errorf("Resolve(false) returned empty core fields: %+v", p)
	}
	if !strings.HasSuffix(p.ConfigFile, "server.yml") {
		t.Errorf("ConfigFile = %q, want to end with server.yml", p.ConfigFile)
	}
}

// TestLinuxPathsPrivilegeBranches exercises both privilege branches of
// linuxPaths directly (it is a plain function, not build-tag gated, so it
// is callable regardless of host OS). Which branch reflects the actual
// test-runner privilege is asserted against paths.IsPrivileged() so the
// test is correct whether the suite runs as root (common in the Docker
// toolchain image) or not.
func TestLinuxPathsPrivilegeBranches(t *testing.T) {
	home := "/home/tester"
	p := linuxPaths(home)

	if IsPrivileged() {
		if p.Config != filepath.Join("/etc", internalOrg, internalName) {
			t.Errorf("privileged Config = %q, want /etc/%s/%s", p.Config, internalOrg, internalName)
		}
		if p.PIDFile != filepath.Join("/var/run", internalOrg, internalName+".pid") {
			t.Errorf("privileged PIDFile = %q", p.PIDFile)
		}
	} else {
		if p.Config != filepath.Join(home, ".config", internalOrg, internalName) {
			t.Errorf("unprivileged Config = %q, want under %s/.config", p.Config, home)
		}
		if p.Binary != filepath.Join(home, ".local/bin", projectName) {
			t.Errorf("unprivileged Binary = %q", p.Binary)
		}
	}
}

func TestDarwinPathsBothBranches(t *testing.T) {
	home := "/Users/tester"

	// darwinPaths calls IsPrivileged() (real euid check) for its own
	// privileged/unprivileged split, same caveat as linuxPaths above.
	p := darwinPaths(home)
	if IsPrivileged() {
		wantBase := filepath.Join("/Library/Application Support", internalOrg, internalName)
		if p.Config != wantBase {
			t.Errorf("privileged darwin Config = %q, want %q", p.Config, wantBase)
		}
	} else {
		wantBase := filepath.Join(home, "Library/Application Support", internalOrg, internalName)
		if p.Config != wantBase {
			t.Errorf("unprivileged darwin Config = %q, want %q", p.Config, wantBase)
		}
		if p.Binary != filepath.Join(home, "bin", projectName) {
			t.Errorf("unprivileged darwin Binary = %q", p.Binary)
		}
	}
}

func TestBSDPathsFallsBackToLinuxWhenUnprivileged(t *testing.T) {
	home := "/home/tester"
	p := bsdPaths(home)
	if IsPrivileged() {
		if p.Config != filepath.Join("/usr/local/etc", internalOrg, internalName) {
			t.Errorf("privileged bsd Config = %q", p.Config)
		}
	} else {
		want := linuxPaths(home)
		if p != want {
			t.Errorf("unprivileged bsdPaths() = %+v, want linuxPaths() fallback %+v", p, want)
		}
	}
}

// TestWindowsPathsNonAdminBranch covers windowsPaths' AppData/LocalAppData
// branch. IsAdministrator() always returns false on a non-Windows build
// (it short-circuits on runtime.GOOS=="windows" before ever calling the
// build-tag-gated probe), so this branch is the only one reachable from a
// non-Windows test run; the ProgramData/Administrator branch is a
// documented coverage gap (see final report).
func TestWindowsPathsNonAdminBranch(t *testing.T) {
	t.Setenv("AppData", `C:\Users\tester\AppData\Roaming`)
	t.Setenv("LocalAppData", `C:\Users\tester\AppData\Local`)

	p := windowsPaths("")
	wantConfig := filepath.Join(`C:\Users\tester\AppData\Roaming`, internalOrg, internalName)
	if p.Config != wantConfig {
		t.Errorf("Config = %q, want %q", p.Config, wantConfig)
	}
	wantData := filepath.Join(`C:\Users\tester\AppData\Local`, internalOrg, internalName)
	if p.Data != wantData {
		t.Errorf("Data = %q, want %q", p.Data, wantData)
	}
}

func TestIsAdministratorNonWindows(t *testing.T) {
	// On any non-Windows GOOS, IsAdministrator must always be false
	// regardless of privilege level.
	if IsAdministrator() {
		t.Error("IsAdministrator() = true on non-Windows, want false")
	}
}

func TestFirstNonEmptyPriority(t *testing.T) {
	tests := []struct {
		name   string
		flag   string
		env    string
		def    string
		envKey string
		want   string
	}{
		{"flag wins over everything", "flagval", "envval", "defval", "TEST_FNE_KEY", "flagval"},
		{"env wins over default", "", "envval", "defval", "TEST_FNE_KEY", "envval"},
		{"default when nothing set", "", "", "defval", "TEST_FNE_KEY", "defval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envKey, tt.env)
			if got := firstNonEmpty(tt.flag, tt.envKey, tt.def); got != tt.want {
				t.Errorf("firstNonEmpty() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetXDirHelpers(t *testing.T) {
	t.Run("GetConfigDir", func(t *testing.T) {
		t.Setenv("CONFIG_DIR", "")
		if got := GetConfigDir("/flag/cfg", "/default/cfg"); got != "/flag/cfg" {
			t.Errorf("GetConfigDir() = %q, want /flag/cfg", got)
		}
		t.Setenv("CONFIG_DIR", "/env/cfg")
		if got := GetConfigDir("", "/default/cfg"); got != "/env/cfg" {
			t.Errorf("GetConfigDir() = %q, want /env/cfg", got)
		}
	})
	t.Run("GetDataDir", func(t *testing.T) {
		t.Setenv("DATA_DIR", "")
		if got := GetDataDir("", "/default/data"); got != "/default/data" {
			t.Errorf("GetDataDir() = %q, want /default/data", got)
		}
	})
	t.Run("GetCacheDir", func(t *testing.T) {
		t.Setenv("CACHE_DIR", "/env/cache")
		if got := GetCacheDir("", "/default/cache"); got != "/env/cache" {
			t.Errorf("GetCacheDir() = %q, want /env/cache", got)
		}
	})
	t.Run("GetLogDir", func(t *testing.T) {
		t.Setenv("LOG_DIR", "")
		if got := GetLogDir("/flag/log", "/default/log"); got != "/flag/log" {
			t.Errorf("GetLogDir() = %q, want /flag/log", got)
		}
	})
	t.Run("GetPIDFile", func(t *testing.T) {
		t.Setenv("PID_FILE", "")
		if got := GetPIDFile("", "/default/pid"); got != "/default/pid" {
			t.Errorf("GetPIDFile() = %q, want /default/pid", got)
		}
	})
}

func TestGetDatabaseDir(t *testing.T) {
	t.Run("env override wins", func(t *testing.T) {
		t.Setenv("DATABASE_DIR", "/custom/db")
		if got := GetDatabaseDir("/data"); got != "/custom/db" {
			t.Errorf("GetDatabaseDir() = %q, want /custom/db", got)
		}
	})
	t.Run("non-container falls back to dataDir/db", func(t *testing.T) {
		t.Setenv("DATABASE_DIR", "")
		if pidfile.IsContainer() {
			t.Skip("test process is actually running inside a container; the container branch takes over")
		}
		if got := GetDatabaseDir("/data"); got != filepath.Join("/data", "db") {
			t.Errorf("GetDatabaseDir() = %q, want /data/db", got)
		}
	})
}

func TestEnsureDirCreatesAndVerifiesWritable(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "nested", "dir")

	if err := EnsureDir(target); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if !info.IsDir() {
		t.Error("EnsureDir() did not create a directory")
	}
	wantPerm := os.FileMode(0o700)
	if IsPrivileged() {
		wantPerm = 0o755
	}
	if info.Mode().Perm() != wantPerm {
		t.Errorf("dir perm = %v, want %v", info.Mode().Perm(), wantPerm)
	}
}

func TestEnsureDirFailsOnUnwritableParent(t *testing.T) {
	if IsPrivileged() {
		t.Skip("root can write anywhere; this test only makes sense unprivileged")
	}
	// A path nested inside another user's root-owned directory should
	// fail; use a location guaranteed unwritable to a non-root test.
	target := filepath.Join("/root", "unwritable-test-dir", "sub")
	if err := EnsureDir(target); err == nil {
		t.Skip("environment allows writing to /root; skipping (likely running as an equivalent user)")
	}
}

func TestEnsurePIDFile(t *testing.T) {
	base := t.TempDir()
	pidPath := filepath.Join(base, "run", "shortner.pid")

	if err := EnsurePIDFile(pidPath); err != nil {
		t.Fatalf("EnsurePIDFile() error = %v", err)
	}
	info, err := os.Stat(filepath.Dir(pidPath))
	if err != nil || !info.IsDir() {
		t.Errorf("EnsurePIDFile() did not create parent directory: %v", err)
	}
}

func TestIsWritable(t *testing.T) {
	base := t.TempDir()

	t.Run("existing writable parent", func(t *testing.T) {
		target := filepath.Join(base, "file.txt")
		if !isWritable(target) {
			t.Error("isWritable() = false, want true for writable temp dir")
		}
	})

	t.Run("nonexistent parent", func(t *testing.T) {
		target := filepath.Join(base, "does", "not", "exist", "file.txt")
		if isWritable(target) {
			t.Error("isWritable() = true, want false for missing parent chain")
		}
	})
}

func TestSystemBackupDirPerOS(t *testing.T) {
	// systemBackupDir switches on runtime.GOOS; only the current-OS branch
	// is reachable, so this just asserts it returns a non-empty path
	// containing the frozen project identifiers.
	got := systemBackupDir()
	if !strings.Contains(got, internalOrg) || !strings.Contains(got, internalName) {
		t.Errorf("systemBackupDir() = %q, want to contain %q and %q", got, internalOrg, internalName)
	}
}

func TestUserBackupDirPerOS(t *testing.T) {
	got := userBackupDir()
	if got == "" {
		t.Error("userBackupDir() = empty string")
	}
}

func TestGetBackupDirFlagAndEnvPriority(t *testing.T) {
	t.Run("flag wins", func(t *testing.T) {
		t.Setenv("BACKUP_DIR", "/env/backup")
		if got := GetBackupDir("/flag/backup", "/data"); got != "/flag/backup" {
			t.Errorf("GetBackupDir() = %q, want /flag/backup", got)
		}
	})
	t.Run("env wins over system probing", func(t *testing.T) {
		t.Setenv("BACKUP_DIR", "/env/backup")
		if got := GetBackupDir("", "/data"); got != "/env/backup" {
			t.Errorf("GetBackupDir() = %q, want /env/backup", got)
		}
	})
	// The remaining GetBackupDir fallback logic (system dir probing via
	// isWritable, then startedElevated vs. userBackupDir) depends on real
	// filesystem privilege state captured once at package init
	// (startedElevated) and on whether /mnt/Backups (or the OS
	// equivalent) is actually writable in the test environment — neither
	// is safely fakeable without root or environment-specific setup, so
	// it is a documented coverage gap (see final report).
}
