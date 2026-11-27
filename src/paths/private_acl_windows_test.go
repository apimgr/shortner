//go:build windows

package paths

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestServiceSIDMatchesWindowsServiceSIDExample(t *testing.T) {
	got, err := serviceSID("ALG")
	if err != nil {
		t.Fatal(err)
	}
	want, err := windows.StringToSid("S-1-5-80-2387347252-3645287876-2469496166-3824418187-3586569773")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equals(want) {
		t.Fatalf("service SID = %v, want %v", got, want)
	}
}
