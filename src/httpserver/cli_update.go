package httpserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/paths"
)

var cliBinaryName = regexp.MustCompile(`^shortner-cli-(linux|darwin|windows|freebsd)-(amd64|arm64)(\.exe)?$`)

type cliBinaryVersion struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type cliAutodiscoverDocument struct {
	ProjectName   string                      `json:"project_name"`
	ServerVersion string                      `json:"server_version"`
	CLIVersions   map[string]cliBinaryVersion `json:"cli_versions"`
	CLIMinVersion string                      `json:"cli_min_version"`
}

type cliUpdateDeps struct {
	cfg       *config.Config
	version   string
	binaryDir string
}

func registerCLIUpdateRoutes(api chi.Router, cfg *config.Config, version string) {
	binaryDir := executableDirectory()
	d := &cliUpdateDeps{cfg: cfg, version: version, binaryDir: binaryDir}
	api.Method(http.MethodGet, "/autodiscover", http.HandlerFunc(d.autodiscoverHandler))
	api.Method(http.MethodHead, "/autodiscover", http.HandlerFunc(d.autodiscoverHandler))
}

func registerCLIBinaryRoute(r chi.Router, cfg *config.Config) {
	handler := http.HandlerFunc((&cliUpdateDeps{cfg: cfg, binaryDir: executableDirectory()}).binaryHandler)
	r.Method(http.MethodGet, "/cli/binaries/{name}", handler)
	r.Method(http.MethodHead, "/cli/binaries/{name}", handler)
}

func executableDirectory() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(executable)
}

func (d *cliUpdateDeps) autodiscoverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	doc := cliAutodiscoverDocument{
		ProjectName:   paths.ProjectName,
		ServerVersion: d.version,
		CLIVersions:   map[string]cliBinaryVersion{},
		CLIMinVersion: d.cfg.Server.Update.CLIMinVersion,
	}
	for _, targetOS := range []string{"linux", "darwin", "windows", "freebsd"} {
		for _, targetArch := range []string{"amd64", "arm64"} {
			key := targetOS + "-" + targetArch
			name := paths.ProjectName + "-cli-" + key
			if targetOS == "windows" {
				name += ".exe"
			}
			path := filepath.Join(d.binaryDir, name)
			sum, ok := hashRegularFile(path)
			if ok {
				doc.CLIVersions[key] = cliBinaryVersion{Version: d.version, SHA256: sum}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	if err := json.NewEncoder(w).Encode(doc); err != nil {
		return
	}
}

func (d *cliUpdateDeps) binaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := chi.URLParam(r, "name")
	if !cliBinaryName.MatchString(name) || strings.ContainsAny(name, `/\\`) {
		http.NotFound(w, r)
		return
	}
	match := cliBinaryName.FindStringSubmatch(name)
	if match[1] != "windows" && match[3] != "" {
		http.NotFound(w, r)
		return
	}
	if match[1] == "windows" && match[3] == "" {
		name += ".exe"
	}
	path := filepath.Join(d.binaryDir, name)
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, filepath.Base(path), info.ModTime().UTC().Round(time.Second), file)
}

func hashRegularFile(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "", false
	}
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", false
	}
	return hex.EncodeToString(h.Sum(nil)), true
}
