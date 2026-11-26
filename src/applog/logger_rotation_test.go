package applog

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRotateIfDueCompressesAndKeepsArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	logger, err := Open(path, LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("closing logger: %v", err)
		}
	})
	if err := logger.SetRotationPolicy("daily", "forever", true); err != nil {
		t.Fatal(err)
	}
	if err := logger.WriteLine(LevelInfo, "archived line\n"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := logger.RotateIfDue(now); err != nil {
		t.Fatal(err)
	}
	archives, err := filepath.Glob(path + ".*.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 {
		t.Fatalf("compressed archives = %d, want 1", len(archives))
	}
	file, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("closing archive: %v", err)
		}
	})
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("closing archive reader: %v", err)
		}
	})
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "archived line") {
		t.Fatalf("archive content = %q", body)
	}
}

func TestRotateIfDueDeletesArchiveWhenKeepNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "access.log")
	logger, err := Open(path, LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("closing logger: %v", err)
		}
	})
	if err := logger.SetRotationPolicy("daily", "none", false); err != nil {
		t.Fatal(err)
	}
	if err := logger.WriteLine(LevelInfo, "discarded line\n"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := logger.RotateIfDue(now); err != nil {
		t.Fatal(err)
	}
	archives, err := filepath.Glob(path + ".*")
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 0 {
		t.Fatalf("archives = %v, want none", archives)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 0 {
		t.Fatalf("new active log has %d bytes, want empty", len(current))
	}
}

func TestRotationPolicyRejectsInvalidSettings(t *testing.T) {
	for _, tc := range []struct{ rotate, keep string }{
		{"", "none"}, {"50XB", "none"}, {"daily", "-1"}, {"never,daily", "none"},
	} {
		if _, err := parseRotationPolicy(tc.rotate, tc.keep, false); err == nil {
			t.Errorf("parseRotationPolicy(%q, %q) succeeded", tc.rotate, tc.keep)
		}
	}
}
