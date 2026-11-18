package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnforceRuntimeModesAppliesGeneralAndPrivateModes(t *testing.T) {
	base := t.TempDir()
	p := Params{
		ConfigDir: filepath.Join(base, "config"),
		DataDir:   filepath.Join(base, "data"),
		DBDir:     filepath.Join(base, "separate-db"),
		CacheDir:  filepath.Join(base, "cache"),
		LogDir:    filepath.Join(base, "logs"),
		BackupDir: filepath.Join(base, "backup"),
		PIDFile:   filepath.Join(base, "run", "shortner.pid"),
	}
	for _, path := range []string{
		filepath.Join(p.ConfigDir, "yaml.d"),
		filepath.Join(p.ConfigDir, "ssl"),
		filepath.Join(p.DataDir, "tor", "site"),
		p.DBDir, p.CacheDir, p.LogDir, p.BackupDir,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]os.FileMode{
		filepath.Join(p.ConfigDir, "server.yml"):           0o644,
		filepath.Join(p.ConfigDir, "yaml.d", "custom.yml"): 0o644,
		filepath.Join(p.ConfigDir, "ssl", "cert.pem"):      0o644,
		filepath.Join(p.DataDir, "tor", "site", "key"):     0o644,
		filepath.Join(p.LogDir, "server.log"):              0o644,
	}
	for path, mode := range files {
		if err := os.WriteFile(path, []byte("test"), mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnforceRuntimeModes(p, true); err != nil {
		t.Fatalf("EnforceRuntimeModes: %v", err)
	}
	for path, want := range map[string]os.FileMode{
		p.ConfigDir:                              0o755,
		p.LogDir:                                 0o755,
		filepath.Join(p.ConfigDir, "server.yml"): 0o600,
		filepath.Join(p.ConfigDir, "yaml.d"):     0o700,
		filepath.Join(p.ConfigDir, "yaml.d", "custom.yml"): 0o600,
		filepath.Join(p.ConfigDir, "ssl"):                  0o700,
		filepath.Join(p.ConfigDir, "ssl", "cert.pem"):      0o600,
		filepath.Join(p.DataDir, "tor"):                    0o700,
		filepath.Join(p.DataDir, "tor", "site"):            0o700,
		filepath.Join(p.DataDir, "tor", "site", "key"):     0o600,
		filepath.Join(p.LogDir, "server.log"):              0o644,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("mode %s = %04o, want %04o", path, got, want)
		}
	}
}

func TestEnforceRuntimeModesUserPathsArePrivate(t *testing.T) {
	base := t.TempDir()
	p := Params{
		ConfigDir: filepath.Join(base, "config"),
		DataDir:   filepath.Join(base, "data"),
		DBDir:     filepath.Join(base, "separate-db"),
		CacheDir:  filepath.Join(base, "cache"),
		LogDir:    filepath.Join(base, "logs"),
		BackupDir: filepath.Join(base, "backup"),
		PIDFile:   filepath.Join(base, "run", "shortner.pid"),
	}
	for _, path := range []string{p.ConfigDir, p.DataDir, p.DBDir, p.CacheDir, p.LogDir, p.BackupDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "data"), []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnforceRuntimeModes(p, false); err != nil {
		t.Fatalf("EnforceRuntimeModes: %v", err)
	}
	for _, path := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.LogDir, p.BackupDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("directory mode %s = %04o, want 0700", path, got)
		}
		info, err = os.Stat(filepath.Join(path, "data"))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("file mode %s = %04o, want 0600", path, got)
		}
	}
}

func TestEnforceRuntimeModesDoesNotModifyPIDParent(t *testing.T) {
	base := t.TempDir()
	pidDir := filepath.Join(base, "shared-run")
	if err := os.Mkdir(pidDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "other.pid"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	p := Params{
		ConfigDir: filepath.Join(base, "config"),
		DataDir:   filepath.Join(base, "data"),
		DBDir:     filepath.Join(base, "db"),
		CacheDir:  filepath.Join(base, "cache"),
		LogDir:    filepath.Join(base, "logs"),
		BackupDir: filepath.Join(base, "backup"),
		PIDFile:   filepath.Join(pidDir, "shortner.pid"),
	}
	for _, root := range runtimeRoots(p) {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnforceRuntimeModes(p, false); err != nil {
		t.Fatalf("EnforceRuntimeModes: %v", err)
	}
	info, err := os.Stat(pidDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("PID parent mode = %04o, want 0750", got)
	}
	info, err = os.Stat(filepath.Join(pidDir, "other.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("unrelated PID parent file mode = %04o, want 0640", got)
	}
}

func TestOwnPIDParentRejectsSharedDirectory(t *testing.T) {
	base := t.TempDir()
	pidDir := filepath.Join(base, "shared-run")
	if err := os.Mkdir(pidDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "other.pid"), []byte("keep"), 0o640); err != nil {
		t.Fatal(err)
	}
	p := Params{PIDFile: filepath.Join(pidDir, "shortner.pid")}
	if err := ownPIDParent(p, os.Getuid()); err == nil {
		t.Fatal("ownPIDParent accepted a directory containing another file")
	}
	info, err := os.Stat(pidDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("shared PID parent mode = %04o, want unchanged 0750", got)
	}
}

func TestSetRuntimeOwnershipRejectsPIDDirectoryBeforeChangingParent(t *testing.T) {
	base := t.TempDir()
	pidDir := filepath.Join(base, "run")
	if err := os.Mkdir(pidDir, 0o750); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(pidDir, "shortner.pid")
	if err := os.Mkdir(pidPath, 0o700); err != nil {
		t.Fatal(err)
	}
	p := Params{PIDFile: pidPath}
	if err := SetRuntimeOwnership(p, os.Getuid()); err == nil {
		t.Fatal("SetRuntimeOwnership accepted a directory as the PID file")
	}
	info, err := os.Stat(pidDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("PID parent mode = %04o, want unchanged 0750", got)
	}
}
