// Package config — duration/size parsing and the config validation rule.
// See AI.md PART 12 "Config Validation Rule": "If config setting is
// invalid, warn and replace with default. Never fail startup."
package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseDuration parses a Go duration string ("30s", "5m", "1h"). An empty
// string returns def. An invalid string returns def and a non-nil error so
// callers can warn and continue, per the Config Validation Rule.
func ParseDuration(s string, def time.Duration) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def, fmt.Errorf("config: invalid duration %q: %w", s, err)
	}
	return d, nil
}

// sizeUnits maps case-insensitive byte-size suffixes to their multiplier.
// Longer suffixes are checked first so "MB" is not misread as "B". The
// single-letter forms exist because AI.md PART 21 writes the backup size
// cap as "50G".
var sizeUnits = []struct {
	suffix string
	mult   int64
}{
	{"TB", 1 << 40},
	{"GB", 1 << 30},
	{"MB", 1 << 20},
	{"KB", 1 << 10},
	{"T", 1 << 40},
	{"G", 1 << 30},
	{"M", 1 << 20},
	{"K", 1 << 10},
	{"B", 1},
}

// ParseSize parses a human byte-size string ("10MB", "512KB", "100") into
// bytes. An empty string returns def. An invalid string returns def and a
// non-nil error, per the Config Validation Rule.
func ParseSize(s string, def int64) (int64, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return def, nil
	}
	upper := strings.ToUpper(trimmed)
	for _, u := range sizeUnits {
		if strings.HasSuffix(upper, u.suffix) {
			numPart := strings.TrimSpace(upper[:len(upper)-len(u.suffix)])
			n, err := strconv.ParseInt(numPart, 10, 64)
			if err != nil || n < 0 {
				return def, fmt.Errorf("config: invalid size %q", s)
			}
			return n * u.mult, nil
		}
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || n < 0 {
		return def, fmt.Errorf("config: invalid size %q", s)
	}
	return n, nil
}

// Validate checks every AI.md PART 12 config value, replacing invalid
// entries with their framework default in place and returning one warning
// message per replacement. Per the "Config Validation Rule": invalid
// config never fails startup — it is logged and defaulted.
func Validate(cfg *Config) []string {
	warnings := ValidateBranding(cfg)
	warnings = append(warnings, ValidateConnectionSettings(cfg)...)
	defaults := Default("")
	contactCaptcha := strings.ToLower(strings.TrimSpace(cfg.Server.Pages.Contact.Captcha))
	switch contactCaptcha {
	case "", "simple":
		cfg.Server.Pages.Contact.Captcha = "simple"
	case "recaptcha", "hcaptcha":
		if strings.TrimSpace(cfg.Server.Pages.Contact.CaptchaSiteKey) == "" || strings.TrimSpace(cfg.Server.Pages.Contact.CaptchaSecretKey) == "" {
			warnings = append(warnings, "invalid server.pages.contact.captcha configuration: provider keys are required")
		}
	default:
		warnings = append(warnings, "invalid server.pages.contact.captcha, using default simple")
		cfg.Server.Pages.Contact.Captcha = defaults.Server.Pages.Contact.Captcha
	}
	diskLow := &cfg.Server.Notifications.DiskLow
	defaultDiskLow := defaults.Server.Notifications.DiskLow
	if diskLow.ThresholdPercent <= 0 || diskLow.ThresholdPercent > 100 {
		warnings = append(warnings, "invalid server.notifications.disk_low.threshold_percent, using default 85")
		diskLow.ThresholdPercent = defaultDiskLow.ThresholdPercent
	}
	if d, err := ParseDuration(diskLow.CheckInterval, 0); err != nil || d <= 0 {
		warnings = append(warnings, "invalid server.notifications.disk_low.check_interval, using default 5m")
		diskLow.CheckInterval = defaultDiskLow.CheckInterval
	}
	if d, err := ParseDuration(diskLow.Repeat, 0); err != nil || d <= 0 {
		warnings = append(warnings, "invalid server.notifications.disk_low.repeat, using default 24h")
		diskLow.Repeat = defaultDiskLow.Repeat
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Server.Mode)) {
	case "production", "prod", "development", "dev", "devel", "debug":
	default:
		warnings = append(warnings, fmt.Sprintf("invalid server.mode %q, using default %q", cfg.Server.Mode, defaults.Server.Mode))
		cfg.Server.Mode = defaults.Server.Mode
	}

	if !validPortSetting(cfg.Server.Port) {
		warnings = append(warnings, fmt.Sprintf("invalid server.port %q, using default %q", cfg.Server.Port, defaults.Server.Port))
		cfg.Server.Port = defaults.Server.Port
	}
	if !ValidServerAddress(cfg.Server.Listen) {
		warnings = append(warnings, fmt.Sprintf("invalid server.address %q, using default %q", cfg.Server.Listen, defaults.Server.Listen))
		cfg.Server.Listen = defaults.Server.Listen
	}

	if _, err := ParseSize(cfg.Server.Limits.MaxBodySize, 0); err != nil {
		warnings = append(warnings, fmt.Sprintf("invalid server.limits.max_body_size %q, using default %q", cfg.Server.Limits.MaxBodySize, defaults.Server.Limits.MaxBodySize))
		cfg.Server.Limits.MaxBodySize = defaults.Server.Limits.MaxBodySize
	}
	for name, field := range map[string]*string{
		"read_timeout":  &cfg.Server.Limits.ReadTimeout,
		"write_timeout": &cfg.Server.Limits.WriteTimeout,
		"idle_timeout":  &cfg.Server.Limits.IdleTimeout,
	} {
		if d, err := ParseDuration(*field, 0); err != nil || d <= 0 {
			def := defaultTimeout(name, defaults)
			warnings = append(warnings, fmt.Sprintf("invalid server.limits.%s %q, using default %q", name, *field, def))
			*field = def
		}
	}

	if cfg.Server.Compression.Level < 1 || cfg.Server.Compression.Level > 9 {
		warnings = append(warnings, fmt.Sprintf("invalid server.compression.level %d, using default %d", cfg.Server.Compression.Level, defaults.Server.Compression.Level))
		cfg.Server.Compression.Level = defaults.Server.Compression.Level
	}
	if len(cfg.Server.Compression.Types) == 0 {
		cfg.Server.Compression.Types = defaults.Server.Compression.Types
	}

	for name, class := range map[string]*RateLimitClass{
		"read":   &cfg.Server.RateLimit.Read,
		"write":  &cfg.Server.RateLimit.Write,
		"health": &cfg.Server.RateLimit.Health,
	} {
		def := defaultRateLimitClass(name, defaults)
		if class.Requests <= 0 {
			warnings = append(warnings, fmt.Sprintf("invalid server.rate_limit.%s.requests %d, using default %d", name, class.Requests, def.Requests))
			class.Requests = def.Requests
		}
		if class.Window <= 0 {
			warnings = append(warnings, fmt.Sprintf("invalid server.rate_limit.%s.window %d, using default %d", name, class.Window, def.Window))
			class.Window = def.Window
		}
	}
	if cfg.Server.RateLimit.GlobalBurst <= 0 {
		warnings = append(warnings, fmt.Sprintf("invalid server.rate_limit.global_burst %d, using default %d", cfg.Server.RateLimit.GlobalBurst, defaults.Server.RateLimit.GlobalBurst))
		cfg.Server.RateLimit.GlobalBurst = defaults.Server.RateLimit.GlobalBurst
	}
	if cfg.Server.Tokens.MaxLinked <= 0 {
		warnings = append(warnings, "invalid server.tokens.max_linked, using default 25")
		cfg.Server.Tokens.MaxLinked = defaults.Server.Tokens.MaxLinked
	}
	if cfg.Server.Tokens.CookieMaxAge == "" {
		cfg.Server.Tokens.CookieMaxAge = defaults.Server.Tokens.CookieMaxAge
	} else if !cfg.Server.Tokens.cookieMaxAgeValid() {
		warnings = append(warnings, fmt.Sprintf("invalid server.tokens.cookie_max_age %q, using default %q", cfg.Server.Tokens.CookieMaxAge, defaults.Server.Tokens.CookieMaxAge))
		cfg.Server.Tokens.CookieMaxAge = defaults.Server.Tokens.CookieMaxAge
	}
	if cfg.Server.Tokens.IdlePurgeDays < 0 {
		warnings = append(warnings, "invalid server.tokens.idle_purge_days, using default 0")
		cfg.Server.Tokens.IdlePurgeDays = 0
	}

	switch cfg.Server.Cache.Type {
	case "", "none", "memory", "valkey", "redis":
		if cfg.Server.Cache.Type == "" {
			cfg.Server.Cache.Type = defaults.Server.Cache.Type
		}
	default:
		warnings = append(warnings, fmt.Sprintf("invalid server.cache.type %q, using default %q", cfg.Server.Cache.Type, defaults.Server.Cache.Type))
		cfg.Server.Cache.Type = defaults.Server.Cache.Type
	}

	warnings = append(warnings, validateBackup(cfg, defaults)...)
	warnings = append(warnings, validateUpdate(cfg, defaults)...)
	warnings = append(warnings, validateScheduler(cfg, defaults)...)
	if v := strings.TrimSpace(cfg.Server.APIVersion); v == "" {
		cfg.Server.APIVersion = defaults.Server.APIVersion
	} else if strings.ContainsAny(v, "/ ?#") {
		warnings = append(warnings, fmt.Sprintf("invalid server.api_version %q, using default %q", v, defaults.Server.APIVersion))
		cfg.Server.APIVersion = defaults.Server.APIVersion
	} else {
		cfg.Server.APIVersion = v
	}
	warnings = append(warnings, validateSecurity(cfg, defaults)...)
	warnings = append(warnings, validateNotifications(cfg, defaults)...)
	warnings = append(warnings, validateWebHeaders(cfg, defaults)...)
	warnings = append(warnings, validateAnnouncements(cfg)...)
	warnings = append(warnings, validateI18N(cfg.Server.I18N)...)
	warnings = append(warnings, validateTor(cfg.Server.Tor)...)
	warnings = append(warnings, validateI2P(cfg.Server.I2P)...)
	warnings = append(warnings, validateSEO(&cfg.Server.SEO)...)
	if cfg.Server.SEO.Feed.MaxItems < 1 || cfg.Server.SEO.Feed.MaxItems > 200 {
		warnings = append(warnings, fmt.Sprintf("invalid server.seo.feed.max_items %d, using default %d", cfg.Server.SEO.Feed.MaxItems, defaults.Server.SEO.Feed.MaxItems))
		cfg.Server.SEO.Feed.MaxItems = defaults.Server.SEO.Feed.MaxItems
	}

	return warnings
}

func validateScheduler(cfg, defaults *Config) []string {
	var warnings []string
	for id, task := range cfg.Server.Scheduler.Tasks {
		if strings.TrimSpace(task.RetryDelay) == "" {
			continue
		}
		delay, err := time.ParseDuration(task.RetryDelay)
		if err == nil && delay > 0 {
			continue
		}
		invalid := task.RetryDelay
		fallback := "5m"
		if def, ok := defaults.Server.Scheduler.Tasks[id]; ok && strings.TrimSpace(def.RetryDelay) != "" {
			fallback = def.RetryDelay
		}
		task.RetryDelay = fallback
		cfg.Server.Scheduler.Tasks[id] = task
		warnings = append(warnings, fmt.Sprintf("invalid server.scheduler.tasks.%s.retry_delay %q, using %q", id, invalid, fallback))
	}
	return warnings
}

// ValidateBranding validates the image-content configuration and visitor
// accent settings. Invalid values are cleared so rendering uses built-in
// defaults, per AI.md PART 5 "Invalid → default".
func ValidateBranding(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var warnings []string
	if cfg.Web.UI.Accent != "" && !IsValidAccent(cfg.Web.UI.Accent) {
		warnings = append(warnings, "invalid web.ui.accent: expected a six-digit #RRGGBB color; using built-in color")
		cfg.Web.UI.Accent = ""
	}
	entries := []struct {
		name  string
		entry *OperatorPageContent
	}{
		{"header", &cfg.Server.Branding.Content.Header},
		{"about", &cfg.Server.Branding.Content.About},
		{"privacy", &cfg.Server.Branding.Content.Privacy},
		{"footer", &cfg.Server.Branding.Content.Footer},
		{"terms", &cfg.Server.Branding.Content.Terms},
	}
	for _, item := range entries {
		if !item.entry.Body.Has {
			continue
		}
		format := strings.ToLower(strings.TrimSpace(item.entry.Format))
		if format == "" {
			format = "md"
			if item.name == "header" || item.name == "footer" {
				format = "html"
			}
			item.entry.Format = format
		}
		validFormat := format == "md" || format == "html" || format == "txt"
		if (item.name == "header" || item.name == "footer") && format != "html" {
			validFormat = false
		}
		bodyValid := func(value string) bool {
			return len(value) <= 256*1024 && !strings.Contains(strings.ToLower(value), "/server/contact")
		}
		if item.entry.Body.Values == nil {
			validFormat = validFormat && bodyValid(item.entry.Body.Plain)
		} else {
			for _, value := range item.entry.Body.Values {
				if !bodyValid(value) {
					validFormat = false
					break
				}
			}
		}
		if !validFormat {
			warnings = append(warnings, fmt.Sprintf("invalid server.branding.content.%s; using built-in content", item.name))
			*item.entry = OperatorPageContent{}
		}
	}
	return warnings
}

func parseAccent(value string) (float64, float64, float64, bool) {
	if len(value) != 7 || value[0] != '#' {
		return 0, 0, 0, false
	}
	color, err := strconv.ParseUint(value[1:], 16, 24)
	if err != nil {
		return 0, 0, 0, false
	}
	return float64((color>>16)&255) / 255, float64((color>>8)&255) / 255, float64(color&255) / 255, true
}

func accentContrast(value string, bg string) float64 {
	red, green, blue, valid := parseAccent(value)
	if !valid {
		return 0
	}
	linear := func(channel float64) float64 {
		if channel <= 0.04045 {
			return channel / 12.92
		}
		return math.Pow((channel+0.055)/1.055, 2.4)
	}
	backgroundRed, backgroundGreen, backgroundBlue, valid := parseAccent(bg)
	if !valid {
		return 0
	}
	luminance := func(r, g, b float64) float64 { return 0.2126*linear(r) + 0.7152*linear(g) + 0.0722*linear(b) }
	foreground, background := luminance(red, green, blue), luminance(backgroundRed, backgroundGreen, backgroundBlue)
	if background > foreground {
		foreground, background = background, foreground
	}
	return (foreground + 0.05) / (background + 0.05)
}

// IsValidAccent reports whether value is a six-digit hexadecimal accent.
func IsValidAccent(value string) bool {
	_, _, _, ok := parseAccent(value)
	return ok
}

// AccentForTheme adjusts only lightness until an accent reaches 4.5:1 against
// the requested shipped theme background, preserving hue and saturation.
func AccentForTheme(value, theme string) string {
	red, green, blue, ok := parseAccent(value)
	if !ok {
		return ""
	}
	background := "#282a36"
	direction := 1.0
	if theme == "light" {
		background, direction = "#ffffff", -1
	}
	if accentContrast(value, background) >= 4.5 {
		return strings.ToUpper(value)
	}
	hue, saturation, lightness := rgbToHSL(red, green, blue)
	for step := 1; step <= 100; step++ {
		candidateLightness := math.Max(0, math.Min(1, lightness+direction*float64(step)/100))
		candidate := hslToHex(hue, saturation, candidateLightness)
		if accentContrast(candidate, background) >= 4.5 {
			return candidate
		}
	}
	return hslToHex(hue, saturation, map[bool]float64{true: 1, false: 0}[theme == "dark"])
}

func rgbToHSL(red, green, blue float64) (float64, float64, float64) {
	maximum, minimum := math.Max(red, math.Max(green, blue)), math.Min(red, math.Min(green, blue))
	lightness := (maximum + minimum) / 2
	delta := maximum - minimum
	if delta == 0 {
		return 0, 0, lightness
	}
	saturation := delta / (1 - math.Abs(2*lightness-1))
	var hue float64
	switch maximum {
	case red:
		hue = math.Mod((green-blue)/delta, 6)
	case green:
		hue = (blue-red)/delta + 2
	default:
		hue = (red-green)/delta + 4
	}
	hue *= 60
	if hue < 0 {
		hue += 360
	}
	return hue, saturation, lightness
}

func hslToHex(hue, saturation, lightness float64) string {
	chroma := (1 - math.Abs(2*lightness-1)) * saturation
	x := chroma * (1 - math.Abs(math.Mod(hue/60, 2)-1))
	m := lightness - chroma/2
	var red, green, blue float64
	switch {
	case hue < 60:
		red, green = chroma, x
	case hue < 120:
		red, green = x, chroma
	case hue < 180:
		green, blue = chroma, x
	case hue < 240:
		green, blue = x, chroma
	case hue < 300:
		red, blue = x, chroma
	default:
		red, blue = chroma, x
	}
	return fmt.Sprintf("#%02X%02X%02X", int(math.Round((red+m)*255)), int(math.Round((green+m)*255)), int(math.Round((blue+m)*255)))
}

// ValidateContactCaptcha rejects an external CAPTCHA provider without both
// required keys, which must fail startup instead of accepting unverified work.
func ValidateContactCaptcha(cfg *Config) error {
	provider := strings.ToLower(strings.TrimSpace(cfg.Server.Pages.Contact.Captcha))
	if provider == "" || provider == "simple" {
		return nil
	}
	if provider != "recaptcha" && provider != "hcaptcha" {
		return fmt.Errorf("server.pages.contact.captcha must be simple, recaptcha, or hcaptcha")
	}
	if strings.TrimSpace(cfg.Server.Pages.Contact.CaptchaSiteKey) == "" || strings.TrimSpace(cfg.Server.Pages.Contact.CaptchaSecretKey) == "" {
		return fmt.Errorf("server.pages.contact.captcha requires server.pages.contact.captcha_site_key and server.pages.contact.captcha_secret_key")
	}
	return nil
}

// ValidServerAddress accepts an IP literal or a DNS hostname suitable for
// net.Listen without resolving it during configuration validation.
func ValidServerAddress(value string) bool {
	address := strings.TrimSpace(value)
	if address == "" {
		return false
	}
	if net.ParseIP(strings.Trim(address, "[]")) != nil {
		return true
	}
	if len(address) > 253 || strings.ContainsAny(address, ":/[]?#") {
		return false
	}
	for _, label := range strings.Split(address, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			default:
				return false
			}
		}
	}
	return true
}

func validateSEO(seo *SEO) []string {
	var warnings []string
	if len([]rune(seo.TitleSeparator)) > 8 {
		seo.TitleSeparator = " - "
		warnings = append(warnings, "invalid server.seo.title_separator; using default")
	}
	if seo.TitleSeparator == "" {
		seo.TitleSeparator = " - "
	}
	if seo.Canonical != "" {
		parsed, err := url.Parse(seo.Canonical)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
			seo.Canonical = ""
			warnings = append(warnings, "invalid server.seo.canonical; using request origin")
		}
	}
	if seo.OpenGraph.Type == "" {
		seo.OpenGraph.Type = "website"
	}
	if seo.Twitter.Card != "summary" && seo.Twitter.Card != "summary_large_image" {
		seo.Twitter.Card = "summary_large_image"
		warnings = append(warnings, "invalid server.seo.twitter.card; using default")
	}
	if len(seo.MetaTags) > 20 {
		warnings = append(warnings, "server.seo.meta_tags exceeds 20 entries; excess entries omitted")
	}
	if seo.MetaTags != nil {
		validMeta := make(map[string]string)
		reserved := map[string]bool{"description": true, "keywords": true, "author": true, "robots": true, "viewport": true}
		for name, content := range seo.MetaTags {
			name = strings.ToLower(strings.TrimSpace(name))
			if len(name) > 256 || name == "" || !regexp.MustCompile(`^[a-z0-9:_.-]+$`).MatchString(name) || reserved[name] || len(content) > 1024 || len(validMeta) >= 20 {
				warnings = append(warnings, "invalid server.seo.meta_tags entry; omitting meta tag")
				continue
			}
			validMeta[name] = content
		}
		seo.MetaTags = validMeta
	}
	providers := []struct {
		name, pattern string
		max           int
		value         *string
	}{
		{"google", `^[a-zA-Z0-9_-]+$`, 43, &seo.Verification.Google},
		{"bing", `^[A-F0-9]+$`, 32, &seo.Verification.Bing},
		{"yandex", `^[a-f0-9]+$`, 32, &seo.Verification.Yandex},
		{"baidu", `^[a-zA-Z0-9]+$`, 32, &seo.Verification.Baidu},
		{"pinterest", `^[a-f0-9]+$`, 32, &seo.Verification.Pinterest},
		{"facebook", `^[a-z0-9]+$`, 64, &seo.Verification.Facebook},
	}
	for _, provider := range providers {
		value := strings.TrimSpace(*provider.value)
		if value != "" && (len(value) > provider.max || !regexp.MustCompile(provider.pattern).MatchString(value)) {
			warnings = append(warnings, fmt.Sprintf("invalid server.seo.verification.%s; omitting verification tag", provider.name))
			*provider.value = ""
		} else {
			*provider.value = value
		}
	}
	namePattern := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	validCustom := seo.Verification.Custom[:0]
	for _, tag := range seo.Verification.Custom {
		name, property, content := strings.TrimSpace(tag.Name), strings.TrimSpace(tag.Property), strings.TrimSpace(tag.Content)
		if (name == "") == (property == "") || content == "" || len([]rune(content)) > 256 || (name != "" && (len(name) > 256 || !namePattern.MatchString(name))) || (property != "" && (len(property) > 256 || !namePattern.MatchString(property))) {
			warnings = append(warnings, "invalid server.seo.verification.custom entry; omitting verification tag")
			continue
		}
		tag.Name, tag.Property, tag.Content = name, property, content
		validCustom = append(validCustom, tag)
	}
	seo.Verification.Custom = validCustom
	return warnings
}

func validateAnnouncements(cfg *Config) []string {
	var warnings []string
	seen := make(map[string]struct{}, len(cfg.Web.Announcements.Messages))
	valid := make([]Announcement, 0, len(cfg.Web.Announcements.Messages))
	for _, item := range cfg.Web.Announcements.Messages {
		problem := ""
		if !validAnnouncementConfigID(item.ID) {
			problem = "id must contain 1-100 letters, digits, hyphens, or underscores"
		} else if item.Message == "" {
			problem = "message must not be empty"
		} else if item.Type != "info" && item.Type != "warning" && item.Type != "error" && item.Type != "success" {
			problem = "type must be info, warning, error, or success"
		} else if _, ok := seen[item.ID]; ok {
			problem = "id must be unique"
		}
		if problem == "" && item.Start != "" {
			if _, err := time.Parse(time.RFC3339, item.Start); err != nil {
				problem = "start must be RFC 3339"
			}
		}
		if problem == "" && item.End != "" {
			if _, err := time.Parse(time.RFC3339, item.End); err != nil {
				problem = "end must be RFC 3339"
			}
		}
		if problem == "" && item.Start != "" && item.End != "" {
			start, _ := time.Parse(time.RFC3339, item.Start)
			end, _ := time.Parse(time.RFC3339, item.End)
			if !start.Before(end) {
				problem = "start must be earlier than end"
			}
		}
		if problem != "" {
			warnings = append(warnings, fmt.Sprintf("invalid web.announcements.messages entry %q: %s, entry ignored", item.ID, problem))
			continue
		}
		seen[item.ID] = struct{}{}
		valid = append(valid, item)
	}
	cfg.Web.Announcements.Messages = valid
	return warnings
}

func validAnnouncementConfigID(id string) bool {
	if len(id) == 0 || len(id) > 100 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// validateSecurity applies the Config Validation Rule to
// `server.security`: every allowlist/blocklist entry must be a valid IP or
// CIDR, overly broad prefixes are rejected outright (an operator cannot
// accidentally allowlist half the internet), and the flood multiplier and
// block duration must parse. Per AI.md PART 11 "IP Block Management" ->
// "Validation".
func validateSecurity(cfg *Config, defaults *Config) []string {
	var warnings []string
	sec := &cfg.Server.Security

	keptAllow := sec.Allowlist[:0]
	for _, e := range sec.Allowlist {
		norm, err := NormalizeCIDR(e.CIDR)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("invalid server.security.allowlist entry %q: %v, entry ignored", e.CIDR, err))
			continue
		}
		e.CIDR = norm
		keptAllow = append(keptAllow, e)
	}
	sec.Allowlist = keptAllow

	keptBlock := sec.BlockedIPs[:0]
	for _, e := range sec.BlockedIPs {
		norm, err := NormalizeCIDR(e.CIDR)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("invalid server.security.blocked_ips entry %q: %v, entry ignored", e.CIDR, err))
			continue
		}
		e.CIDR = norm
		keptBlock = append(keptBlock, e)
	}
	sec.BlockedIPs = keptBlock

	ad := &sec.AbuseDetection
	dad := defaults.Server.Security.AbuseDetection
	if ad.RequestFlood.Multiplier < 2 {
		warnings = append(warnings, fmt.Sprintf("invalid server.security.abuse_detection.request_flood.multiplier %d (must be >= 2), using default %d",
			ad.RequestFlood.Multiplier, dad.RequestFlood.Multiplier))
		ad.RequestFlood.Multiplier = dad.RequestFlood.Multiplier
	}
	if _, err := ParseDuration(ad.RequestFlood.BlockDuration, 0); err != nil {
		warnings = append(warnings, fmt.Sprintf("invalid server.security.abuse_detection.request_flood.block_duration %q, using default %q",
			ad.RequestFlood.BlockDuration, dad.RequestFlood.BlockDuration))
		ad.RequestFlood.BlockDuration = dad.RequestFlood.BlockDuration
	}

	return warnings
}

// validateWebHeaders applies the Config Validation Rule to `web.hsts`,
// `web.headers`, `web.csp`, and `web.reports`. Per AI.md PART 11 "Security
// Header Config", an HSTS max-age below one year makes the site
// preload-ineligible and a max-age of 0 disables HSTS entirely — both are
// legal, both warn.
func validateWebHeaders(cfg *Config, defaults *Config) []string {
	var warnings []string

	h := &cfg.Web.HSTS
	dh := defaults.Web.HSTS
	switch {
	case h.MaxAgeSeconds < 0:
		warnings = append(warnings, fmt.Sprintf("invalid web.hsts.max_age_seconds %d, using default %d", h.MaxAgeSeconds, dh.MaxAgeSeconds))
		h.MaxAgeSeconds = dh.MaxAgeSeconds
	case h.Enabled && h.MaxAgeSeconds == 0:
		warnings = append(warnings, "web.hsts.max_age_seconds is 0 — HSTS is disabled entirely")
	case h.Enabled && h.MaxAgeSeconds < 31536000:
		warnings = append(warnings, fmt.Sprintf("web.hsts.max_age_seconds %d is below one year — this site is not eligible for the HSTS preload list", h.MaxAgeSeconds))
	}

	hd := &cfg.Web.Headers
	dhd := defaults.Web.Headers
	hd.COOP = validateEnum("web.headers.coop", hd.COOP, dhd.COOP,
		[]string{"unsafe-none", "same-origin", "same-origin-allow-popups"}, &warnings)
	hd.COEP = validateEnum("web.headers.coep", hd.COEP, dhd.COEP,
		[]string{"unsafe-none", "require-corp", "credentialless"}, &warnings)
	hd.CORP = validateEnum("web.headers.corp", hd.CORP, dhd.CORP,
		[]string{"same-site", "same-origin", "cross-origin"}, &warnings)
	hd.CrossDomainPolicies = validateEnum("web.headers.cross_domain_policies", hd.CrossDomainPolicies, dhd.CrossDomainPolicies,
		[]string{"none", "master-only", "by-content-type", "all"}, &warnings)
	if hd.DNSPrefetchControl != "" && hd.DNSPrefetchControl != "on" && hd.DNSPrefetchControl != "off" {
		warnings = append(warnings, fmt.Sprintf("invalid web.headers.dns_prefetch_control %q (valid: \"\", \"on\", \"off\"), omitting header", hd.DNSPrefetchControl))
		hd.DNSPrefetchControl = ""
	}
	if hd.NEL.SampleRate < 0 || hd.NEL.SampleRate > 1 {
		warnings = append(warnings, fmt.Sprintf("invalid web.headers.nel.sample_rate %v (valid range 0.0-1.0), using default %v", hd.NEL.SampleRate, dhd.NEL.SampleRate))
		hd.NEL.SampleRate = dhd.NEL.SampleRate
	}
	if hd.NEL.MaxAgeSeconds < 0 {
		warnings = append(warnings, fmt.Sprintf("invalid web.headers.nel.max_age_seconds %d, using default %d", hd.NEL.MaxAgeSeconds, dhd.NEL.MaxAgeSeconds))
		hd.NEL.MaxAgeSeconds = dhd.NEL.MaxAgeSeconds
	}

	csp := &cfg.Web.CSP
	dcsp := defaults.Web.CSP
	csp.Mode = validateEnum("web.csp.mode", csp.Mode, dcsp.Mode, []string{"enforce", "report-only"}, &warnings)
	if csp.ReportsSampleRate < 0 || csp.ReportsSampleRate > 1 {
		warnings = append(warnings, fmt.Sprintf("invalid web.csp.reports_sample_rate %v (valid range 0.0-1.0), using default %v", csp.ReportsSampleRate, dcsp.ReportsSampleRate))
		csp.ReportsSampleRate = dcsp.ReportsSampleRate
	}

	rep := &cfg.Web.Reports
	drep := defaults.Web.Reports
	if rep.RateLimitPerMinute <= 0 {
		warnings = append(warnings, fmt.Sprintf("invalid web.reports.rate_limit_per_minute %d, using default %d", rep.RateLimitPerMinute, drep.RateLimitPerMinute))
		rep.RateLimitPerMinute = drep.RateLimitPerMinute
	}
	if rep.RateLimitPerIPBurst <= 0 {
		warnings = append(warnings, fmt.Sprintf("invalid web.reports.rate_limit_per_ip_burst %d, using default %d", rep.RateLimitPerIPBurst, drep.RateLimitPerIPBurst))
		rep.RateLimitPerIPBurst = drep.RateLimitPerIPBurst
	}

	if cfg.Web.WellKnown.UnsupportedBehavior != 404 {
		warnings = append(warnings, "web.well_known.unsupported_behavior is fixed at 404 by the Well-Known Namespace Contract; the configured value is ignored")
		cfg.Web.WellKnown.UnsupportedBehavior = 404
	}

	return warnings
}

// validateEnum returns value when it is one of allowed (or def when it is
// empty), appending a warning and returning def otherwise.
func validateEnum(name, value, def string, allowed []string, warnings *[]string) string {
	if value == "" {
		return def
	}
	for _, a := range allowed {
		if value == a {
			return value
		}
	}
	*warnings = append(*warnings, fmt.Sprintf("invalid %s %q (valid: %s), using default %q", name, value, strings.Join(allowed, ", "), def))
	return def
}

// NormalizeCIDR validates an allowlist/blocklist entry and expands a bare
// IP to its single-host prefix (/32 for IPv4, /128 for IPv6), per AI.md
// PART 11 "IP Block Management" -> "Validation". Overly broad prefixes
// (/0-/7 for IPv4, /0-/15 for IPv6) are rejected: the spec asks for a
// confirmation step, and a config file has no way to prompt, so refusing
// the entry is the only fail-safe reading.
func NormalizeCIDR(entry string) (string, error) {
	s := strings.TrimSpace(entry)
	if s == "" {
		return "", fmt.Errorf("empty entry")
	}
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return "", fmt.Errorf("not a valid IP address")
		}
		if ip.To4() != nil {
			return ip.String() + "/32", nil
		}
		return ip.String() + "/128", nil
	}

	_, network, err := net.ParseCIDR(s)
	if err != nil {
		return "", fmt.Errorf("not a valid CIDR: %w", err)
	}
	ones, bits := network.Mask.Size()
	if bits == 32 && ones < 8 {
		return "", fmt.Errorf("prefix /%d is too broad (minimum /8 for IPv4)", ones)
	}
	if bits == 128 && ones < 16 {
		return "", fmt.Errorf("prefix /%d is too broad (minimum /16 for IPv6)", ones)
	}
	return network.String(), nil
}

// validateUpdate applies AI.md PART 22 "Update Configuration" to
// `server.update`: the branch must name one of the three release channels
// and defer_days must fall in the documented 0-365 window. Both follow the
// PART 12 Config Validation Rule — warn and replace, never fail startup.
func validateUpdate(cfg *Config, defaults *Config) []string {
	var warnings []string
	u := &cfg.Server.Update
	du := defaults.Server.Update

	switch u.Branch {
	case "stable", "beta", "daily":
	case "":
		u.Branch = du.Branch
	default:
		warnings = append(warnings, fmt.Sprintf("invalid server.update.branch %q, using default %q", u.Branch, du.Branch))
		u.Branch = du.Branch
	}

	if u.DeferDays < 0 || u.DeferDays > 365 {
		warnings = append(warnings, fmt.Sprintf("invalid server.update.defer_days %d (valid range 0-365), using default %d", u.DeferDays, du.DeferDays))
		u.DeferDays = du.DeferDays
	}

	return warnings
}

// backupRetentionField describes one `server.backup.retention` counter for
// the shared validate-then-warn loop below.
type backupRetentionField struct {
	name  string
	field *int
	def   int
	// min is the lowest accepted value (1 for max_backups, which AI.md
	// PART 21 documents as "≥1"; 0 for the optional tiers).
	min int
	// warnAbove is the AI.md PART 21 "Warning Thresholds" recommendation;
	// values above it are accepted but warned about.
	warnAbove int
	// unit renders the parenthetical in the "exceeds recommended" warning.
	unit func(n int) string
}

// validateBackup applies AI.md PART 21 "Validation (warn, don't error -
// server must start)" and "Warning Thresholds (accept but warn)" to
// `server.backup`. Invalid values are replaced with their default in
// place; out-of-recommendation values are kept as written.
func validateBackup(cfg *Config, defaults *Config) []string {
	var warnings []string
	r := &cfg.Server.Backup.Retention
	dr := defaults.Server.Backup.Retention

	fields := []backupRetentionField{
		{"max_backups", &r.MaxBackups, dr.MaxBackups, 1, 7, func(n int) string {
			return fmt.Sprintf("%d days of daily backups", n)
		}},
		{"keep_weekly", &r.KeepWeekly, dr.KeepWeekly, 0, 8, func(n int) string {
			return fmt.Sprintf("%d weeks of weekly backups", n)
		}},
		{"keep_monthly", &r.KeepMonthly, dr.KeepMonthly, 0, 12, func(n int) string {
			if n%12 == 0 {
				return fmt.Sprintf("%d years of monthly backups", n/12)
			}
			return fmt.Sprintf("%d months of monthly backups", n)
		}},
		{"keep_yearly", &r.KeepYearly, dr.KeepYearly, 0, 2, func(n int) string {
			return fmt.Sprintf("%d years of yearly backups", n)
		}},
	}

	for _, f := range fields {
		if *f.field < f.min {
			warnings = append(warnings, fmt.Sprintf("server.backup.retention.%s: %d invalid, using default %d", f.name, *f.field, f.def))
			*f.field = f.def
			continue
		}
		if *f.field > f.warnAbove {
			warnings = append(warnings, fmt.Sprintf("server.backup.retention.%s: %d exceeds recommended %d (%s)", f.name, *f.field, f.warnAbove, f.unit(*f.field)))
		}
	}

	if _, err := ParseRetentionSize(r.MaxTotalSize, 0); err != nil {
		warnings = append(warnings, fmt.Sprintf("server.backup.retention.max_total_size: %q invalid, using default %q", r.MaxTotalSize, dr.MaxTotalSize))
		r.MaxTotalSize = dr.MaxTotalSize
	}

	threshold := &cfg.Server.Backup.DiskThreshold
	if *threshold < 1 || *threshold > 100 {
		warnings = append(warnings, fmt.Sprintf("server.backup.disk_threshold: %d invalid, using default %d", *threshold, defaults.Server.Backup.DiskThreshold))
		*threshold = defaults.Server.Backup.DiskThreshold
	}

	return warnings
}

// RetentionSize is a parsed `server.backup.retention.max_total_size`.
// Exactly one of Percent/Bytes is meaningful; Disabled wins over both.
type RetentionSize struct {
	Disabled bool
	// Percent is a whole percentage of the backup volume's total size
	// (e.g. 10 for "10%").
	Percent int
	// Bytes is an absolute cap (e.g. "50G" -> 50 * 1000^3).
	Bytes int64
}

// ParseRetentionSize parses AI.md PART 21's `max_total_size` value: a
// percentage of the backup volume ("10%"), an absolute size ("50G"), or
// any of the PART 5 falsy values (0, no, false, off, disable, disabled,
// ...), which disable the cap. def is returned alongside the error when
// the value is unparseable, so callers can warn-and-default per PART 21
// "Validation (warn, don't error)".
func ParseRetentionSize(s string, def int64) (RetentionSize, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || IsFalsy(trimmed) {
		return RetentionSize{Disabled: true}, nil
	}

	if strings.HasSuffix(trimmed, "%") {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(trimmed, "%")))
		if err != nil || n <= 0 || n > 100 {
			return RetentionSize{Bytes: def}, fmt.Errorf("config: invalid max_total_size %q", s)
		}
		return RetentionSize{Percent: n}, nil
	}

	n, err := ParseSize(trimmed, 0)
	if err != nil || n <= 0 {
		return RetentionSize{Bytes: def}, fmt.Errorf("config: invalid max_total_size %q", s)
	}
	return RetentionSize{Bytes: n}, nil
}

// validPortSetting accepts AI.md's single-port, OS-assigned, and HTTP,HTTPS
// forms. Port zero is meaningful only for a single listener because a dual
// listener pair must expose two configured service ports.
func validPortSetting(s string) bool {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) < 1 || len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		port, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || port < 1 || port > 65535 {
			if len(parts) == 1 && err == nil && port == 0 {
				continue
			}
			return false
		}
	}
	return true
}

// defaultTimeout returns the framework default for one of the three
// server.limits.*_timeout fields, by name.
func defaultTimeout(name string, defaults *Config) string {
	switch name {
	case "read_timeout":
		return defaults.Server.Limits.ReadTimeout
	case "write_timeout":
		return defaults.Server.Limits.WriteTimeout
	default:
		return defaults.Server.Limits.IdleTimeout
	}
}

// defaultRateLimitClass returns the framework default for one of the three
// server.rate_limit.* classes, by name.
func defaultRateLimitClass(name string, defaults *Config) RateLimitClass {
	switch name {
	case "read":
		return defaults.Server.RateLimit.Read
	case "write":
		return defaults.Server.RateLimit.Write
	default:
		return defaults.Server.RateLimit.Health
	}
}
