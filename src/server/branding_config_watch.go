package server

import (
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
)

func (s *Server) startBrandingConfigWatcher(configPath, dbPath string) {
	ctx, cancel := context.WithCancel(context.Background())
	s.brandingWatcherCancel = cancel
	s.brandingWatcherDone = make(chan struct{})
	initialSignature := brandingConfigSignature(configPath)
	go func() {
		defer close(s.brandingWatcherDone)
		lastSignature := initialSignature
		lastFailed := ""
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			signature := brandingConfigSignature(configPath)
			if signature == lastSignature || signature == lastFailed {
				continue
			}
			loaded, err := config.Load(configPath, dbPath)
			if err != nil {
				lastFailed = signature
				s.logBrandingConfigReloadError()
				continue
			}
			for _, warning := range config.ValidateBranding(loaded) {
				s.logBrandingConfigWarning(warning)
			}
			s.brandingState.store(loaded)
			lastSignature, lastFailed = signature, ""
		}
	}()
}

func (s *Server) logBrandingConfigWarning(message string) {
	if s.contactDeps == nil || s.contactDeps.audit == nil {
		return
	}
	_ = s.contactDeps.audit.Write(applog.Entry{
		Time: time.Now().UTC(), Event: "config.value_invalid", Category: "system",
		Severity: applog.SeverityWarn,
		Details:  map[string]any{"area": "branding", "key": message},
		Result:   applog.ResultFailure,
	})
}

func brandingConfigSignature(configPath string) string {
	h := fnv.New64a()
	paths := []string{configPath}
	paths = append(paths, filepath.Join(filepath.Dir(configPath), "secrets.yml"))
	dropDir := filepath.Join(filepath.Dir(configPath), "yaml.d")
	entries, err := os.ReadDir(dropDir)
	if err == nil {
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
				continue
			}
			paths = append(paths, filepath.Join(dropDir, entry.Name()))
		}
	}
	assetsDir := filepath.Join(filepath.Dir(configPath), "assets")
	_ = filepath.WalkDir(assetsDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	for _, path := range paths {
		info, statErr := os.Stat(path)
		_, _ = h.Write([]byte(path))
		if statErr != nil {
			_, _ = h.Write([]byte("missing"))
			continue
		}
		_, _ = h.Write([]byte(info.ModTime().UTC().Format(time.RFC3339Nano)))
		_, _ = h.Write([]byte{byte(info.Size()), byte(info.Size() >> 8), byte(info.Size() >> 16), byte(info.Size() >> 24)})
	}
	return string(h.Sum(nil))
}

func (s *Server) logBrandingConfigReloadError() {
	if s.contactDeps == nil || s.contactDeps.audit == nil {
		return
	}
	_ = s.contactDeps.audit.Write(applog.Entry{
		Time: time.Now().UTC(), Event: "config.reload_failed", Category: "system",
		Severity: applog.SeverityWarn,
		Details:  map[string]any{"area": "branding", "error_category": "read_or_parse"},
		Result:   applog.ResultFailure,
	})
}
