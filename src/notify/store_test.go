package notify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRawPrefersCustom(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	raw, custom, err := s.Raw(EventTest)
	if err != nil {
		t.Fatalf("Raw(embedded): %v", err)
	}
	if custom {
		t.Error("Raw() reported custom with no override on disk")
	}
	if raw == "" {
		t.Error("embedded template is empty")
	}

	if _, err := s.Save(EventTest, "Subject: mine\n---\nbody\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, custom, err = s.Raw(EventTest)
	if err != nil {
		t.Fatalf("Raw(custom): %v", err)
	}
	if !custom {
		t.Error("Raw() did not report the override as custom")
	}
	if raw != "Subject: mine\n---\nbody\n" {
		t.Errorf("Raw() = %q, want the saved override", raw)
	}

	if err := s.Reset(EventTest); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if s.IsCustom(EventTest) {
		t.Error("IsCustom() still true after Reset")
	}
	// AI.md PART 17: resetting a template that was never overridden is not
	// an error.
	if err := s.Reset(EventTest); err != nil {
		t.Errorf("Reset on a missing override = %v, want nil", err)
	}
}

func TestStoreRejectsUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	tests := []string{
		"../escape",
		"/etc/passwd",
		"nested/name",
		"Upper",
		"",
		"..",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			if got := s.CustomPath(name); got != "" {
				t.Errorf("CustomPath(%q) = %q, want empty", name, got)
			}
			if _, _, err := s.Raw(name); err == nil {
				t.Errorf("Raw(%q) = nil error, want rejection", name)
			}
			if _, err := s.Save(name, "Subject: s\n---\nb\n"); err == nil {
				t.Errorf("Save(%q) = nil error, want rejection", name)
			}
		})
	}
}

func TestStoreSaveValidates(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	tests := []struct {
		name     string
		raw      string
		wantErr  bool
		wantWarn bool
	}{
		{name: "valid", raw: "Subject: ok {app_name}\n---\n{ip}\n"},
		{name: "unparseable", raw: "no separator", wantErr: true},
		{name: "unknown variable", raw: "Subject: s\n---\n{nope_at_all}\n", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := s.Save(EventSecurityAlert, tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Save() = nil error, want rejection")
				}
				if v.OK() {
					t.Error("Validation.OK() = true on a rejected save")
				}
				return
			}
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			written, readErr := os.ReadFile(filepath.Join(dir, "template", "email", EventSecurityAlert+".tmpl"))
			if readErr != nil {
				t.Fatalf("read back: %v", readErr)
			}
			if string(written) != tc.raw {
				t.Errorf("written = %q, want %q", written, tc.raw)
			}
		})
	}
}

func TestStoreLoadFallsBackOnBrokenOverride(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	// Write a broken override directly, bypassing Save's validation, the
	// way an operator hand-editing the file would.
	tmplDir := filepath.Join(dir, "template", "email")
	if err := os.MkdirAll(tmplDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, EventTest+".tmpl"), []byte("broken"), 0o640); err != nil {
		t.Fatal(err)
	}

	tmpl, err := s.Load(EventTest)
	if err == nil {
		t.Error("Load() = nil error, want the parse failure reported")
	}
	// A broken override must never silence the notification: the embedded
	// default is returned alongside the error.
	if tmpl.Subject == "" || tmpl.Body == "" {
		t.Fatalf("Load() = %+v, want the embedded default as a fallback", tmpl)
	}
}

func TestNewStoreWithoutConfigDir(t *testing.T) {
	s := NewStore("")

	if got := s.CustomPath(EventTest); got != "" {
		t.Errorf("CustomPath() = %q, want empty with no config dir", got)
	}
	if s.IsCustom(EventTest) {
		t.Error("IsCustom() = true with no config dir")
	}
	if _, err := s.Save(EventTest, "Subject: s\n---\nb\n"); err == nil {
		t.Error("Save() = nil error, want a refusal with no config dir")
	}
	// Embedded defaults must still resolve — this is the CLI's situation.
	if _, err := s.Load(EventTest); err != nil {
		t.Errorf("Load() = %v, want the embedded default", err)
	}
}

func TestEmbeddedTemplatesAreValid(t *testing.T) {
	// AI.md PART 17 "Sane Defaults": every default template must work
	// out-of-the-box, which means parsing cleanly and using only variables
	// its own event actually supplies.
	s := &Store{}
	for _, name := range EventNames() {
		t.Run(name, func(t *testing.T) {
			raw, _, err := s.Raw(name)
			if err != nil {
				t.Fatalf("Raw: %v", err)
			}
			v := ValidateRaw(name, raw)
			if !v.OK() {
				t.Fatalf("embedded template %s is invalid: %v", name, v.Errors)
			}
			if len(v.Warnings) > 0 {
				t.Errorf("embedded template %s warns: %v", name, v.Warnings)
			}
		})
	}
}
