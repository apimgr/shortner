package notify

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apimgr/shortner/src/server"
)

// embeddedDir is where the binary's built-in templates live inside
// server.TemplateFS, per AI.md PART 17 "Template Storage".
const embeddedDir = "template/email"

// Store resolves email templates, per AI.md PART 17 "Template Storage":
// a custom file under `{config_dir}/template/email/` wins, otherwise the
// embedded default is used. Deleting the custom file resets the template.
//
// Nothing is cached: every Load re-reads the custom file, which is what
// makes "Changes take effect immediately (live reload)" true without a
// restart or a watcher.
type Store struct {
	// Dir is `{config_dir}/template/email`.
	Dir string
}

// NewStore returns a Store rooted at the config directory. An empty
// configDir disables custom overrides entirely (embedded defaults only),
// which is what CLI paths that never resolved a config dir get.
func NewStore(configDir string) *Store {
	if configDir == "" {
		return &Store{}
	}
	return &Store{Dir: filepath.Join(configDir, "template", "email")}
}

// fileName returns the on-disk file name for a template, rejecting any
// name that is not a plain lowercase identifier. This is the only place a
// caller-supplied template name reaches the filesystem, so path traversal
// is stopped here rather than trusted to callers.
func fileName(name string) (string, error) {
	if !validVarName(name) {
		return "", fmt.Errorf("notify: invalid template name %q", name)
	}
	return name + ".tmpl", nil
}

// CustomPath returns the absolute path a custom override for name would
// occupy, or "" when custom overrides are disabled.
func (s *Store) CustomPath(name string) string {
	if s == nil || s.Dir == "" {
		return ""
	}
	file, err := fileName(name)
	if err != nil {
		return ""
	}
	return filepath.Join(s.Dir, file)
}

// IsCustom reports whether an operator override exists for name.
func (s *Store) IsCustom(name string) bool {
	path := s.CustomPath(name)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Raw returns the template source for name and whether it came from a
// custom override.
func (s *Store) Raw(name string) (string, bool, error) {
	if path := s.CustomPath(name); path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			return string(data), true, nil
		case !os.IsNotExist(err):
			return "", false, fmt.Errorf("notify: read custom template %s: %w", path, err)
		}
	}

	file, err := fileName(name)
	if err != nil {
		return "", false, err
	}
	data, err := server.TemplateFS.ReadFile(embeddedDir + "/" + file)
	if err != nil {
		return "", false, fmt.Errorf("notify: no template named %q", name)
	}
	return string(data), false, nil
}

// Load parses the effective template for name. A custom override that
// fails to parse falls back to the embedded default rather than silencing
// the notification entirely — a mistyped template must never be the reason
// a backup failure goes unreported. The parse error is returned alongside
// the usable template so the caller can log it.
func (s *Store) Load(name string) (Template, error) {
	raw, custom, err := s.Raw(name)
	if err != nil {
		return Template{}, err
	}
	t, parseErr := ParseTemplate(name, raw)
	if parseErr == nil {
		return t, nil
	}
	if !custom {
		return Template{}, parseErr
	}
	fallback, embedErr := s.embedded(name)
	if embedErr != nil {
		return Template{}, parseErr
	}
	return fallback, parseErr
}

// embedded parses the built-in default for name, ignoring any override.
func (s *Store) embedded(name string) (Template, error) {
	file, err := fileName(name)
	if err != nil {
		return Template{}, err
	}
	data, err := server.TemplateFS.ReadFile(embeddedDir + "/" + file)
	if err != nil {
		return Template{}, fmt.Errorf("notify: no template named %q", name)
	}
	return ParseTemplate(name, string(data))
}

// Save validates raw and writes it as the custom override for name, per
// AI.md PART 17 "Template Validation" ("Templates are validated before
// saving"). Warnings are returned but never block the write.
func (s *Store) Save(name, raw string) (Validation, error) {
	if s == nil || s.Dir == "" {
		return Validation{}, fmt.Errorf("notify: custom templates are unavailable (no config directory)")
	}
	file, err := fileName(name)
	if err != nil {
		return Validation{}, err
	}
	v := ValidateRaw(name, raw)
	if !v.OK() {
		return v, fmt.Errorf("notify: template %s: %s", name, v.Errors[0])
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return v, fmt.Errorf("notify: create %s: %w", s.Dir, err)
	}
	path := filepath.Join(s.Dir, file)
	if err := os.WriteFile(path, []byte(raw), 0o640); err != nil {
		return v, fmt.Errorf("notify: write %s: %w", path, err)
	}
	return v, nil
}

// Reset removes the custom override for name, restoring the embedded
// default — AI.md PART 17 "Reset to default → delete custom file". A
// template that was never overridden is already at its default, so a
// missing file is not an error.
func (s *Store) Reset(name string) error {
	path := s.CustomPath(name)
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("notify: reset %s: %w", path, err)
	}
	return nil
}
