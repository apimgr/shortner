package service

import "testing"

func TestContainsWord(t *testing.T) {
	out := " shortner |      default"
	if !containsWord(out, "shortner") {
		t.Error("containsWord did not find an enabled service")
	}
	// A longer name that merely starts with the service name must not match.
	if containsWord(" shortner-cli |      default", "shortner") {
		t.Error("containsWord matched a different service with the same prefix")
	}
	if containsWord("", "shortner") {
		t.Error("containsWord matched empty output")
	}
}

func TestHasPrefixWord(t *testing.T) {
	if !hasPrefixWord("run: shortner: (pid 1234) 5s", "run") {
		t.Error("hasPrefixWord did not match a running service")
	}
	if hasPrefixWord("down: shortner: 3s, normally up", "run") {
		t.Error("hasPrefixWord matched a down service")
	}
	if hasPrefixWord("", "run") {
		t.Error("hasPrefixWord matched empty output")
	}
}

func TestRunitPID(t *testing.T) {
	if got := runitPID("run: shortner: (pid 1234) 5s; run: log: (pid 1235) 5s"); got != 1234 {
		t.Errorf("runitPID = %d, want 1234", got)
	}
	if got := runitPID("down: shortner: 3s, normally up"); got != 0 {
		t.Errorf("runitPID = %d, want 0 for a stopped service", got)
	}
	if got := runitPID("run: shortner: (pid notanumber) 5s"); got != 0 {
		t.Errorf("runitPID = %d, want 0 for unparsable output", got)
	}
}

func TestIsYes(t *testing.T) {
	for _, value := range []string{"YES", "yes", " Yes ", "TRUE", "on", "1"} {
		if !isYes(value) {
			t.Errorf("isYes(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"NO", "no", "", "0", "off", "maybe"} {
		if isYes(value) {
			t.Errorf("isYes(%q) = true, want false", value)
		}
	}
}

func TestLaunchdPID(t *testing.T) {
	out := "state = running\n\tpid = 4321\n\tprogram = /usr/local/bin/shortner\n"
	if got := launchdPID(out); got != 4321 {
		t.Errorf("launchdPID = %d, want 4321", got)
	}
	if got := launchdPID("state = not running"); got != 0 {
		t.Errorf("launchdPID = %d, want 0", got)
	}
}

func TestLaunchdListPID(t *testing.T) {
	out := "{\n\t\"PID\" = 4321;\n\t\"Label\" = \"io.github.apimgr.shortner\";\n}"
	if got := launchdListPID(out); got != 4321 {
		t.Errorf("launchdListPID = %d, want 4321", got)
	}
	if got := launchdListPID("{\n\t\"Label\" = \"io.github.apimgr.shortner\";\n}"); got != 0 {
		t.Errorf("launchdListPID = %d, want 0", got)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/marker"
	if fileExists(path) {
		t.Error("fileExists = true before the file was created")
	}
	if err := writeFile(path, "x\n", 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if !fileExists(path) {
		t.Error("fileExists = false after the file was created")
	}
}

// TestWriteFileCreatesParents proves service files land even when their
// directory (e.g. /etc/sv/{name}/log) does not exist yet.
func TestWriteFileCreatesParents(t *testing.T) {
	path := t.TempDir() + "/etc/sv/shortner/log/run"
	if err := writeFile(path, "#!/bin/sh\n", 0o755); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if !fileExists(path) {
		t.Error("writeFile did not create the nested file")
	}
}

func TestRemoveFilesIgnoresMissing(t *testing.T) {
	dir := t.TempDir()
	present := dir + "/present"
	if err := writeFile(present, "x\n", 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if err := removeFiles(present, dir+"/absent"); err != nil {
		t.Fatalf("removeFiles: %v", err)
	}
	if fileExists(present) {
		t.Error("removeFiles left the file in place")
	}
}
