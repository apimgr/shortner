package config

import (
	"net"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
)

var environmentReference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// ApplyConnectionEnvironment expands environment references after config
// merging, then applies the connection-string environment overrides and
// derives the database/cache drivers from their URL schemes.
func ApplyConnectionEnvironment(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	expandConfigEnvironment(reflect.ValueOf(cfg))
	warnings := []string{}
	if value, ok := os.LookupEnv("DATABASE_URL"); ok && strings.TrimSpace(value) != "" {
		if cfg.databaseURLConfigured {
			if value != cfg.Server.Database.URL {
				warnings = append(warnings, "DATABASE_URL differs from the saved database connection and was ignored")
			}
		} else {
			cfg.Server.Database.URL = value
			cfg.databaseURLEnv = true
			cfg.databaseURLSeeded = true
		}
	}
	if value, ok := os.LookupEnv("CACHE_URL"); ok && strings.TrimSpace(value) != "" {
		if cfg.cacheURLConfigured {
			if value != cfg.Server.Cache.URL {
				warnings = append(warnings, "CACHE_URL differs from the saved cache connection and was ignored")
			}
		} else {
			cfg.Server.Cache.URL = value
			cfg.cacheURLEnv = true
			cfg.cacheURLSeeded = true
		}
	}
	databaseScheme := connectionScheme(cfg.Server.Database.URL)
	if databaseScheme != "" {
		derived := ""
		switch strings.ToLower(databaseScheme) {
		case "libsql", "https", "http":
			derived = "libsql"
		case "file", "sqlite", "sqlite2", "sqlite3":
			derived = "sqlite"
		}
		if derived != "" {
			if explicit := strings.TrimSpace(os.Getenv("DATABASE_DRIVER")); explicit != "" && !strings.EqualFold(explicit, derived) {
				warnings = append(warnings, "DATABASE_DRIVER disagrees with DATABASE_URL scheme; the URL scheme selects the database driver")
			}
			cfg.Server.Database.Driver = derived
		}
	} else if explicit := strings.TrimSpace(os.Getenv("DATABASE_DRIVER")); !cfg.databaseURLConfigured && explicit != "" {
		cfg.Server.Database.Driver = explicit
	} else if explicit := strings.TrimSpace(os.Getenv("DATABASE_DRIVER")); cfg.databaseURLConfigured && explicit != "" && !strings.EqualFold(explicit, cfg.Server.Database.Driver) {
		warnings = append(warnings, "DATABASE_DRIVER differs from the saved database driver and was ignored")
	}
	cacheScheme := strings.ToLower(connectionScheme(cfg.Server.Cache.URL))
	switch cacheScheme {
	case "redis", "rediss":
		cfg.Server.Cache.Type = "redis"
		cfg.Server.Cache.TLS = cacheScheme == "rediss"
	case "valkey", "valkeys":
		cfg.Server.Cache.Type = "valkey"
		cfg.Server.Cache.TLS = cacheScheme == "valkeys"
	}
	if query := connectionQuery(cfg.Server.Cache.URL); strings.EqualFold(query.Get("tls"), "true") || strings.EqualFold(query.Get("sslmode"), "require") || strings.EqualFold(query.Get("sslmode"), "verify-full") || strings.EqualFold(query.Get("encrypt"), "true") {
		cfg.Server.Cache.TLS = true
	}
	if value, present := connectionQuery(cfg.Server.Cache.URL)["tls_skip_verify"]; present && len(value) > 0 {
		cfg.Server.Cache.TLSSkipVerify = !strings.EqualFold(value[0], "false")
	}
	if cfg.Server.Cache.TLS && !cfg.Server.Cache.tlsSkipVerifySet && cfg.Server.Cache.TLSCA == "" {
		if _, present := connectionQuery(cfg.Server.Cache.URL)["tls_skip_verify"]; !present {
			cfg.Server.Cache.TLSSkipVerify = true
		}
	}
	return warnings
}

// HasSeededConnection reports whether an init-only connection variable
// supplied a first-run value that must be written to the config files.
func HasSeededConnection(cfg *Config) bool {
	return cfg != nil && (cfg.databaseURLSeeded || cfg.cacheURLSeeded)
}

// ValidateConnectionSettings reports non-secret connection URL errors without
// replacing a rejected URL with a fallback database or cache driver.
func ValidateConnectionSettings(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	warnings := []string{}
	databaseURL := strings.TrimSpace(cfg.Server.Database.URL)
	if looksLikeUnsupportedConnectionString(databaseURL) {
		warnings = append(warnings, "invalid server.database.url; use the URL form")
	} else if scheme := strings.ToLower(connectionScheme(databaseURL)); scheme != "" {
		switch scheme {
		case "libsql", "https":
		case "http":
			u, _ := url.Parse(databaseURL)
			if u == nil || !privateConnectionHost(u.Hostname()) {
				warnings = append(warnings, "invalid server.database.url; use HTTPS for non-private hosts")
			}
		case "file", "sqlite", "sqlite2", "sqlite3":
		default:
			warnings = append(warnings, "invalid server.database.url; use the URL form")
		}
	} else if driver := strings.ToLower(strings.TrimSpace(cfg.Server.Database.Driver)); driver != "" && driver != "sqlite" && driver != "sqlite2" && driver != "sqlite3" {
		warnings = append(warnings, "invalid server.database.url; use the URL form")
	}
	cacheURL := strings.TrimSpace(cfg.Server.Cache.URL)
	if looksLikeUnsupportedConnectionString(cacheURL) {
		warnings = append(warnings, "invalid server.cache.url; use the URL form")
	} else if scheme := strings.ToLower(connectionScheme(cacheURL)); scheme != "" {
		switch scheme {
		case "redis", "rediss", "valkey", "valkeys":
		default:
			warnings = append(warnings, "invalid server.cache.url; use the URL form")
		}
	}
	return warnings
}

// DatabaseConnectionURLValid reports whether the database URL selects a
// supported scheme and obeys the private-host rule for plain HTTP.
func DatabaseConnectionURLValid(cfg *Config) bool {
	for _, warning := range ValidateConnectionSettings(cfg) {
		if strings.HasPrefix(warning, "invalid server.database.url") {
			return false
		}
	}
	return true
}

func looksLikeUnsupportedConnectionString(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "jdbc:") || strings.HasPrefix(lower, "server=") || strings.HasPrefix(lower, "host=") || strings.Contains(lower, ";password=")
}

func expandConfigEnvironment(value reflect.Value) {
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Pointer {
		if !value.IsNil() {
			expandConfigEnvironment(value.Elem())
		}
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if value.Field(index).CanSet() {
				expandConfigEnvironment(value.Field(index))
			}
		}
	case reflect.String:
		value.SetString(environmentReference.ReplaceAllStringFunc(value.String(), func(match string) string {
			parts := environmentReference.FindStringSubmatch(match)
			if actual, ok := os.LookupEnv(parts[1]); ok {
				return actual
			}
			return parts[2]
		}))
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			expandConfigEnvironment(value.Index(index))
		}
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String || value.Type().Elem().Kind() != reflect.String {
			return
		}
		for _, key := range value.MapKeys() {
			copy := reflect.New(value.Type().Elem()).Elem()
			copy.SetString(expandEnvironmentString(value.MapIndex(key).String()))
			value.SetMapIndex(key, copy)
		}
	}
}

func expandEnvironmentString(value string) string {
	return environmentReference.ReplaceAllStringFunc(value, func(match string) string {
		parts := environmentReference.FindStringSubmatch(match)
		if actual, ok := os.LookupEnv(parts[1]); ok {
			return actual
		}
		return parts[2]
	})
}

func connectionScheme(raw string) string {
	if isWindowsConnectionPath(raw) {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Scheme
}

func isWindowsConnectionPath(raw string) bool {
	value := strings.TrimSpace(raw)
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func connectionQuery(raw string) url.Values {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return url.Values{}
	}
	return u.Query()
}

func connectionHasCredentials(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			return true
		}
	}
	query := u.Query()
	for key, values := range query {
		if values == nil || values[0] == "" {
			continue
		}
		switch strings.ToLower(key) {
		case "password", "pwd", "token", "authtoken":
			return true
		}
	}
	return false
}

func privateConnectionHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	addresses, err := net.LookupIP(host)
	if err != nil || len(addresses) == 0 {
		return false
	}
	for _, address := range addresses {
		if !address.IsLoopback() && !address.IsPrivate() {
			return false
		}
	}
	return true
}
