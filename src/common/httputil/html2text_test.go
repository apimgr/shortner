package httputil

import (
	"strings"
	"testing"
)

func TestHTML2TextConverter(t *testing.T) {
	got := HTML2TextConverter(`<html><body><h1>Links</h1><p>Hello <strong>world</strong>.</p><form><label>Secret</label><input value="hidden"><button>Submit</button></form><script>hidden()</script><ul><li>First</li><li>Second</li></ul></body></html>`, 80)
	for _, want := range []string{"LINKS", "Hello *world*.", "• First", "• Second"} {
		if !strings.Contains(got, want) {
			t.Errorf("converted text %q does not contain %q", got, want)
		}
	}
	for _, forbidden := range []string{"Secret", "Submit", "hidden"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("converted text %q contains non-readable content %q", got, forbidden)
		}
	}
}
