package server

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/apimgr/shortner/src/apperr"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/tor"
)

type torController interface {
	Running() bool
	OnionAddress() string
	Healthy() bool
	CircuitEstablished() bool
	PID() int
	BootstrapPercent() int
	Version() string
	Restart() error
	RegenerateAddress() (string, error)
	ApplyKeys([]byte) (string, error)
	StartVanity(string, int) error
	StopVanity() bool
	VanityStatus() tor.VanityStatus
}

type torControlRequest struct {
	Confirm bool   `json:"confirm"`
	Prefix  string `json:"prefix"`
	Workers int    `json:"workers"`
	Address string `json:"address"`
	Path    string `json:"path"`
}

func registerTorControlRoutes(r interface{ Handle(string, http.Handler) }, manager torController, cfg config.Tor, dirs tor.Dirs) {
	if manager == nil {
		return
	}
	r.Handle("/server/tor/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !isLoopbackPeer(req.RemoteAddr) {
			http.NotFound(w, req)
			return
		}
		path := strings.TrimPrefix(req.URL.Path, "/server/tor/")
		if req.Method == http.MethodGet && path == "status" {
			vanity := manager.VanityStatus()
			data := map[string]any{"running": manager.Running(), "version": manager.Version(), "bootstrap_percent": manager.BootstrapPercent(), "circuit_established": manager.CircuitEstablished(), "vanity": vanity}
			if manager.Running() {
				data["pid"] = manager.PID()
			}
			if manager.OnionAddress() != "" {
				data["onion_address"] = manager.OnionAddress()
			}
			writeControlSuccess(w, http.StatusOK, data)
			return
		}
		if req.Method != http.MethodPost {
			http.NotFound(w, req)
			return
		}
		if path == "validate" {
			if requestBodyPresent(req) {
				writeControlError(w, apperr.CodeBadRequest)
				return
			}
			warnings := config.ValidateTor(cfg)
			errors := make([]map[string]string, 0, len(warnings)+1)
			for _, warning := range warnings {
				errors = append(errors, map[string]string{"field": "server.tor", "message": warning})
			}
			if _, err := tor.ResolveBinary(cfg); err != nil {
				errors = append(errors, map[string]string{"field": "server.tor.binary", "message": err.Error()})
			}
			writeControlSuccess(w, http.StatusOK, map[string]any{"valid": len(errors) == 0, "errors": errors})
			return
		}
		var body torControlRequest
		if path != "restart" && path != "vanity/stop" {
			decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4096))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&body) != nil {
				writeControlError(w, apperr.CodeBadRequest)
				return
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				writeControlError(w, apperr.CodeBadRequest)
				return
			}
		} else if requestBodyPresent(req) {
			writeControlError(w, apperr.CodeBadRequest)
			return
		}
		switch path {
		case "restart":
			if err := manager.Restart(); err != nil {
				writeControlError(w, apperr.CodeServerError)
				return
			}
			writeControlSuccess(w, 200, map[string]any{"restarted": true, "onion_address": manager.OnionAddress()})
		case "regenerate":
			if !body.Confirm {
				writeControlError(w, apperr.CodeValidationFailed)
				return
			}
			previous := manager.OnionAddress()
			address, err := manager.RegenerateAddress()
			if err != nil {
				writeControlError(w, apperr.CodeServerError)
				return
			}
			writeControlSuccess(w, 200, map[string]any{"onion_address": address, "previous_address": previous})
		case "vanity/start":
			if body.Workers == 0 {
				body.Workers = runtime.NumCPU() - 1
				if body.Workers < 1 {
					body.Workers = 1
				}
			}
			if err := manager.StartVanity(body.Prefix, body.Workers); err != nil {
				writeControlError(w, apperr.CodeValidationFailed)
				return
			}
			writeControlSuccess(w, 202, map[string]any{"state": "running", "prefix": strings.ToLower(body.Prefix), "workers": body.Workers})
		case "vanity/stop":
			stopped := manager.StopVanity()
			writeControlSuccess(w, 200, map[string]any{"stopped": stopped, "candidates": tor.ListVanity(dirs.Data)})
		case "vanity/apply":
			if !body.Confirm {
				writeControlError(w, apperr.CodeValidationFailed)
				return
			}
			address, err := selectVanityCandidate(dirs.Data, body.Address)
			if err != nil {
				if errors.Is(err, errAmbiguousVanityCandidate) {
					writeControlErrorDetails(w, apperr.CodeConflict, map[string]any{"candidates": tor.ListVanity(dirs.Data)})
				} else {
					writeControlError(w, apperr.CodeNotFound)
				}
				return
			}
			secret, err := os.ReadFile(filepath.Join(tor.VanityDir(dirs.Data, address), "hs_ed25519_secret_key"))
			if err != nil {
				writeControlError(w, apperr.CodeServerError)
				return
			}
			newAddress, err := manager.ApplyKeys(secret)
			if err != nil {
				writeControlError(w, apperr.CodeServerError)
				return
			}
			_ = os.RemoveAll(tor.VanityDir(dirs.Data, address))
			writeControlSuccess(w, 200, map[string]any{"onion_address": newAddress})
		case "import-keys":
			if !body.Confirm || !filepath.IsAbs(body.Path) {
				writeControlError(w, apperr.CodeValidationFailed)
				return
			}
			secret, err := os.ReadFile(filepath.Join(body.Path, "hs_ed25519_secret_key"))
			if err != nil || tor.ValidateSecretKeyFile(secret) != nil {
				writeControlError(w, apperr.CodeValidationFailed)
				return
			}
			address, err := manager.ApplyKeys(secret)
			if err != nil {
				writeControlError(w, apperr.CodeServerError)
				return
			}
			writeControlSuccess(w, 200, map[string]any{"onion_address": address})
		default:
			http.NotFound(w, req)
		}
	}))
}

var errAmbiguousVanityCandidate = errors.New("multiple vanity candidates match")

func requestBodyPresent(r *http.Request) bool { return r.Body != nil && r.Body != http.NoBody }

func isLoopbackPeer(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writeControlSuccess(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"ok": true, "data": data})
}
func writeControlError(w http.ResponseWriter, code apperr.Code) {
	apperr.SendError(w, apperr.New(code))
}

func writeControlErrorDetails(w http.ResponseWriter, code apperr.Code, details map[string]any) {
	apperr.SendError(w, apperr.New(code).WithDetails(details))
}

func selectVanityCandidate(dataDir, requested string) (string, error) {
	items := tor.ListVanity(dataDir)
	requested = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(requested)), ".onion")
	var matches []string
	for _, item := range items {
		name := strings.TrimSuffix(filepath.Base(item), ".onion")
		if requested == "" || strings.HasPrefix(name, requested) {
			matches = append(matches, filepath.Base(item))
		}
	}
	if len(matches) == 0 {
		return "", os.ErrNotExist
	}
	if len(matches) > 1 {
		return "", errAmbiguousVanityCandidate
	}
	return matches[0], nil
}
