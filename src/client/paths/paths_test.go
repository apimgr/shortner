package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveIsUserScoped(t *testing.T) {
	p := Resolve("apimgr", "shortner", "")
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.LogDir} {
		for _, forbidden := range []string{"/etc/", "/var/lib/", "/var/log/", `C:\ProgramData`} {
			if strings.Contains(dir, forbidden) {
				t.Fatalf("system path %q leaked into %q", forbidden, dir)
			}
		}
	}
	if filepath.Base(p.ConfigFile) != "cli.yml" {
		t.Fatalf("default config file is %q", p.ConfigFile)
	}
	if filepath.Base(p.LogFile) != "cli.log" {
		t.Fatalf("default log file is %q", p.LogFile)
	}
}

func TestResolveHonorsXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG variables are not used on Windows")
	}
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	p := Resolve("apimgr", "shortner", "")
	if p.ConfigDir != filepath.Join(dir, "apimgr", "shortner") {
		t.Fatalf("got %q", p.ConfigDir)
	}
}

func TestResolveConfigPathBareName(t *testing.T) {
	got := ResolveConfigPath("test", "/cfg")
	if got != filepath.Join("/cfg", "test.yml") {
		t.Fatalf("got %q", got)
	}
}

func TestResolveConfigPathExplicitExtension(t *testing.T) {
	got := ResolveConfigPath("dev.yaml", "/cfg")
	if got != filepath.Join("/cfg", "dev.yaml") {
		t.Fatalf("got %q", got)
	}
}

func TestResolveConfigPathAbsolute(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "custom.yml")
	if got := ResolveConfigPath(target, "/cfg"); got != target {
		t.Fatalf("got %q", got)
	}
}

func TestResolveYamlExtensionPrefersExistingYaml(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "profile")
	if err := os.WriteFile(base+".yaml", []byte("---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := resolveYamlExtension(base); got != base+".yaml" {
		t.Fatalf("got %q", got)
	}
}

func TestEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	p := Paths{
		ConfigDir:  filepath.Join(dir, "config"),
		DataDir:    filepath.Join(dir, "data"),
		CacheDir:   filepath.Join(dir, "cache"),
		LogDir:     filepath.Join(dir, "log"),
		ConfigFile: filepath.Join(dir, "config", "cli.yml"),
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	info, err := os.Stat(p.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("config dir perms are %v", info.Mode().Perm())
	}
}

func TestSecureFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cli.yml")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SecureFile(path); err != nil {
		t.Fatalf("SecureFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("perms are %v", info.Mode().Perm())
	}
}
