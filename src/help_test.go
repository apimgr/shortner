package main

import (
	"strings"
	"testing"
)

func TestPrintHelp(t *testing.T) {
	out, _, code := captureOutput(t, func() int {
		printHelp("shortner")
		return 0
	})
	if code != 0 {
		t.Fatalf("unexpected code %d", code)
	}
	for _, want := range []string{"shortner", projectDescription, "Usage:", "--shell", "--service", "--maintenance", "--update"} {
		if !strings.Contains(out, want) {
			t.Errorf("printHelp() output missing %q; got %q", want, out)
		}
	}
}

func TestPrintVersion(t *testing.T) {
	out, _, code := captureOutput(t, func() int {
		printVersion("shortner")
		return 0
	})
	if code != 0 {
		t.Fatalf("unexpected code %d", code)
	}
	if !strings.HasPrefix(out, "shortner ") {
		t.Errorf("printVersion() output = %q, want prefix %q", out, "shortner ")
	}
}
