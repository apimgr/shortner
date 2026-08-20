package security

import "testing"

// TestIsBotUserAgent covers the empty/whitespace-only UA case (counted as
// a bot), known crawler/tool UA substrings, and ordinary browser UAs that
// must not be misclassified.
func TestIsBotUserAgent(t *testing.T) {
	cases := map[string]bool{
		"":    true,
		"   ": true,
		"Googlebot/2.1 (+http://www.google.com/bot.html)": true,
		"Mozilla/5.0 (compatible; bingbot/2.0)":           true,
		"curl/8.4.0":                                      true,
		"python-requests/2.31.0":                          true,
		"facebookexternalhit/1.1":                         true,
		"Slackbot-LinkExpanding 1.0":                      true,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36": false,
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15":                                     false,
	}
	for ua, want := range cases {
		if got := IsBotUserAgent(ua); got != want {
			t.Errorf("IsBotUserAgent(%q) = %v, want %v", ua, got, want)
		}
	}
}

func TestIsBotUserAgentCaseInsensitive(t *testing.T) {
	if !IsBotUserAgent("MOZILLA/5.0 CLAUDEBOT/1.0") {
		t.Error("IsBotUserAgent(uppercase bot token) = false, want true")
	}
}
