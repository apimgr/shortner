package security

import "strings"

// botTokens are the case-insensitive User-Agent substrings that mark a
// request as an automated crawler, feed reader, link-preview fetcher, or
// uptime monitor.
//
// Neither AI.md nor IDEA.md defines a canonical list — IDEA.md states the
// rule ("Click tracking excludes known bot/crawler user agents") without
// naming the agents — so this is the conventional set of self-identifying
// tokens: the ones crawlers put in their own UA strings by convention.
// It is deliberately a substring allowlist of well-known identifiers
// rather than a behavioral heuristic: a heuristic would silently
// misclassify real visitors, and an undercount of bots is far less
// harmful than dropping a human's click.
var botTokens = []string{
	"bot",
	"crawl",
	"spider",
	"scraper",
	"archiver",
	"slurp",
	"fetcher",
	"monitor",
	"preview",
	"validator",
	"feedburner",
	"curl/",
	"wget/",
	"python-requests",
	"python-urllib",
	"go-http-client",
	"java/",
	"okhttp",
	"libwww-perl",
	"httpclient",
	"headlesschrome",
	"phantomjs",
	"puppeteer",
	"playwright",
	"lighthouse",
	"pingdom",
	"uptimerobot",
	"statuscake",
	"newrelicpinger",
	"datadog",
	"prometheus",
	"facebookexternalhit",
	"whatsapp",
	"telegrambot",
	"slackbot",
	"discordbot",
	"twitterbot",
	"linkedinbot",
	"embedly",
	"quora link preview",
	"redditbot",
	"applebot",
	"ia_archiver",
	"semrush",
	"ahrefs",
	"mj12bot",
	"dotbot",
	"petalbot",
	"bytespider",
	"gptbot",
	"claudebot",
	"ccbot",
	"perplexitybot",
}

// IsBotUserAgent reports whether ua identifies an automated client whose
// requests must be excluded from click analytics, per IDEA.md's business
// logic ("Click tracking excludes known bot/crawler user agents").
//
// An empty User-Agent counts as a bot: every real browser sends one, and
// omitting it is itself a strong automation signal.
func IsBotUserAgent(ua string) bool {
	trimmed := strings.TrimSpace(ua)
	if trimmed == "" {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, token := range botTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}
