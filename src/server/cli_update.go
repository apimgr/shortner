package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/fqdn"
	"github.com/apimgr/shortner/src/paths"
)

var cliBinaryName = regexp.MustCompile(`^shortner-cli-(linux|darwin|windows|freebsd)-(amd64|arm64)(\.exe)?$`)

type cliBinaryVersion struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type cliAutodiscoverDocument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	API     struct {
		CurrentVersion string `json:"current_version"`
	} `json:"api"`
	URLs          []string                    `json:"urls"`
	Onion         string                      `json:"onion,omitempty"`
	I2P           string                      `json:"i2p,omitempty"`
	CLIVersions   map[string]cliBinaryVersion `json:"cli_versions"`
	CLIMinVersion string                      `json:"cli_min_version"`
}

type cliUpdateDeps struct {
	cfg       *config.Config
	version   string
	binaryDir string
	tor       TorReporter
	i2p       I2PReporter
	resolver  *ProxyResolver
	audit     *applog.AuditLogger
}

func (d *cliUpdateDeps) autodiscoverHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	doc := cliAutodiscoverDocument{
		Name:          paths.ProjectName,
		Version:       d.version,
		CLIVersions:   map[string]cliBinaryVersion{},
		CLIMinVersion: d.cfg.Server.Update.CLIMinVersion,
	}
	doc.API.CurrentVersion = d.cfg.Server.APIVersion
	doc.URLs = d.autodiscoverOrigins(r)
	if d.cfg.Server.Tor.Expose && d.tor != nil {
		doc.Onion = d.tor.OnionAddress()
	}
	if d.cfg.Server.I2P.Expose && d.i2p != nil {
		doc.I2P = d.i2p.EepsiteAddress()
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
	if err := json.NewEncoder(w).Encode(struct {
		OK   bool                    `json:"ok"`
		Data cliAutodiscoverDocument `json:"data"`
	}{true, doc}); err != nil {
		return
	}
}

func (d *cliUpdateDeps) autodiscoverOrigins(r *http.Request) []string {
	if d.resolver == nil {
		return nil
	}
	proto, host, port := d.resolver.GetURLVars(r)
	if host == "" {
		return nil
	}
	seen := make(map[string]struct{})
	origins := make([]string, 0, len(fqdn.GetAllDomains())+1)
	add := func(name, port string) {
		name = strings.TrimSpace(name)
		if name == "" || strings.HasSuffix(strings.ToLower(name), ".onion") || strings.HasSuffix(strings.ToLower(name), ".b32.i2p") {
			return
		}
		if port != "" && !isDefaultPort(proto, port) {
			name = net.JoinHostPort(name, port)
		}
		origin := proto + "://" + name
		if _, ok := seen[origin]; ok {
			return
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	add(host, port)
	for _, domain := range fqdn.GetAllDomains() {
		add(domain, "")
	}
	return origins
}

func (d *cliUpdateDeps) binaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.cfg != nil && d.cfg.CLI.BinaryDownload.RequireAuth {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(d.cfg.Server.Token)) != 1 {
			d.auditRejectedCLIToken(r, provided)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("cli update: close downloaded binary %q: %v", filepath.Base(path), err)
		}
	}()
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

func (d *cliUpdateDeps) auditRejectedCLIToken(r *http.Request, token string) {
	if d.audit == nil || !strings.HasPrefix(r.UserAgent(), paths.ProjectName+"-cli/") {
		return
	}
	details := map[string]any{}
	if token != "" {
		details["token_prefix"] = applog.MaskToken(token)
	}
	ip := r.RemoteAddr
	if d.resolver != nil {
		ip = d.resolver.ResolveClientIP(r)
	} else if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	_ = d.audit.Write(applog.Entry{
		Event:    "cli.token_rejected",
		Category: "security",
		Severity: applog.SeverityWarn,
		Actor:    applog.Actor{IP: ip},
		Details:  details,
		Source:   "cli",
		Result:   applog.ResultFailure,
	})
}

func hashRegularFile(path string) (sum string, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() {
		if err := file.Close(); err != nil {
			sum = ""
			ok = false
		}
	}()
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
