package config

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const serverConfigHeader = "# Settings of this node only (listen, identity, database, cache, ssl). Everything else lives in yaml.d/: branding.yml, web.yml, pages.yml, contact.yml, privacy.yml, security.yml, logs.yml, metrics.yml, notifications.yml, tor.yml, i2p.yml, maintenance.yml, accounts.yml, app.yml. Credentials live in secrets.yml.\n"

var nodeServerKeys = map[string]bool{
	"port": true, "address": true, "fqdn": true, "mode": true, "api_version": true,
	"baseurl": true, "pidfile": true, "daemonize": true,
	"debug": true, "database": true, "cache": true, "ssl": true, "trusted_proxies": true, "cluster": true,
}

var serverAreaKeys = map[string]string{
	"branding": "branding", "seo": "branding", "cors": "web", "csrf": "web", "url_detection": "web",
	"compression": "web", "healthz": "web", "i18n": "web", "pages": "pages", "contact": "contact",
	"privacy": "privacy", "compliance": "privacy", "tracking": "privacy", "security": "security",
	"rate_limit": "security", "geoip": "security", "limits": "security", "tokens": "security",
	"logs": "logs", "metrics": "metrics", "notifications": "notifications", "tor": "tor", "i2p": "i2p",
	"scheduler": "maintenance", "update": "maintenance", "auto_update": "maintenance", "backup": "maintenance",
	"restore": "maintenance",
}

// MigrateAreaFiles separates area settings from server.yml. It is safe to run
// on every startup and leaves the merged configuration values unchanged.
func MigrateAreaFiles(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	doc, err := parseConfigDocument(data)
	if err != nil {
		return err
	}
	root := doc.Content[0]
	legacyChanged := migrateLegacyConfig(root)
	server := yamlMapValue(root, "server")
	if server == nil {
		server = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "server"}, server)
	}
	areas := map[string]*yaml.Node{}
	for _, name := range []string{"branding", "web", "pages", "contact", "privacy", "security", "logs", "metrics", "notifications", "tor", "i2p", "maintenance", "app"} {
		areas[name] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	movedAny := legacyChanged
	for i := 0; i+1 < len(server.Content); {
		key, value := server.Content[i], server.Content[i+1]
		if nodeServerKeys[key.Value] {
			i += 2
			continue
		}
		if dropInDefinesServerKey(filepath.Dir(path), key.Value) {
			i += 2
			continue
		}
		area := serverAreaKeys[key.Value]
		if area == "" {
			area = "app"
		}
		appendNodeMap(serverArea(areas[area]), key, value)
		movedAny = true
		server.Content = append(server.Content[:i], server.Content[i+2:]...)
	}
	for i := 0; i+1 < len(root.Content); {
		key, value := root.Content[i], root.Content[i+1]
		if key.Value == "server" {
			i += 2
			continue
		}
		movedAny = true
		switch key.Value {
		case "web":
			webArea := areas["web"]
			brandingArea := areas["branding"]
			for j := 0; j+1 < len(value.Content); j += 2 {
				if value.Content[j].Value == "ui" {
					appendNodeMap(webAreaMap(brandingArea), value.Content[j], value.Content[j+1])
				} else {
					appendNodeMap(webArea, value.Content[j], value.Content[j+1])
				}
			}
		case "pages":
			appendNodeMap(serverArea(areas["pages"]), key, value)
		case "tor":
			appendNodeMap(areas["tor"], key, value)
		case "data":
			appendNodeMap(areas["security"], key, value)
		default:
			appendNodeMap(areas["app"], key, value)
		}
		root.Content = append(root.Content[:i], root.Content[i+2:]...)
	}
	serverData, err := encodeYAML(doc)
	if err != nil {
		return err
	}
	serverData = []byte(serverConfigHeader + strings.TrimLeft(string(serverData), "\n"))
	changed := movedAny
	if changed {
		backupPath := path + ".bak"
		if err := writeAtomic(backupPath, data, 0o600); err != nil {
			return fmt.Errorf("config: preserve pre-migration server.yml: %w", err)
		}
	}
	for area, mapping := range areas {
		if len(mapping.Content) == 0 {
			continue
		}
		areaPath := filepath.Join(filepath.Dir(path), "yaml.d", area+".yml")
		if err := os.MkdirAll(filepath.Dir(areaPath), 0o700); err != nil {
			return err
		}
		merged, err := mergeAreaFile(areaPath, mapping)
		if err != nil {
			return err
		}
		if err := writeAtomic(areaPath, merged, 0o600); err != nil {
			return err
		}
	}
	if !changed {
		return nil
	}
	return writeAtomic(path, serverData, 0o600)
}

var legacyAppSecretNames = []string{
	"installation_secret", "installation_secret_previous", "installation_secret_previous_expires",
	"encryption_key_previous", "encryption_key_previous_expires",
}

// ReadLegacyAppSecrets returns legacy project secrets from the merged config
// sources so startup can import them into app_secrets before removing them.
func ReadLegacyAppSecrets(path string) (map[string]string, error) {
	files, err := migrationConfigFiles(path)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	for _, source := range files {
		data, err := os.ReadFile(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		doc, err := parseConfigDocument(data)
		if err != nil {
			return nil, fmt.Errorf("config: parse %s for legacy app secrets: %w", source, err)
		}
		for _, name := range legacyAppSecretNames {
			value := yamlPathValue(doc.Content[0], "server.security."+name)
			if value != nil && value.Kind == yaml.ScalarNode && value.Value != "" {
				values[name] = value.Value
			}
		}
	}
	return values, nil
}

// RemoveLegacyAppSecrets removes migrated project secrets from all config
// sources, retaining a mode-0600 backup of every changed file.
func RemoveLegacyAppSecrets(path string) error {
	files, err := migrationConfigFiles(path)
	if err != nil {
		return err
	}
	for _, source := range files {
		data, err := os.ReadFile(source)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		doc, err := parseConfigDocument(data)
		if err != nil {
			return fmt.Errorf("config: parse %s while removing legacy app secrets: %w", source, err)
		}
		changed := false
		for _, name := range legacyAppSecretNames {
			if removeYAMLPath(doc.Content[0], "server.security."+name) {
				changed = true
			}
		}
		if !changed {
			continue
		}
		if err := writeAtomic(source+".bak", data, 0o600); err != nil {
			return err
		}
		updated, err := encodeYAML(doc)
		if err != nil {
			return err
		}
		if err := writeAtomic(source, updated, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func migrationConfigFiles(path string) ([]string, error) {
	files := []string{path}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(path), "yaml.d"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		files = append(files, filepath.Join(filepath.Dir(path), "yaml.d", entry.Name()))
	}
	sort.Strings(files[1:])
	return files, nil
}

func dropInDefinesServerKey(configDir, key string) bool {
	entries, err := os.ReadDir(filepath.Join(configDir, "yaml.d"))
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(configDir, "yaml.d", entry.Name()))
		if err != nil {
			continue
		}
		doc, err := parseConfigDocument(data)
		if err != nil {
			continue
		}
		if server := yamlMapValue(doc.Content[0], "server"); server != nil && yamlMapValue(server, key) != nil {
			return true
		}
	}
	return false
}

func migrateLegacyConfig(root *yaml.Node) bool {
	changed := migrateLegacyDatabase(root)
	for _, move := range [][2]string{
		{"server.logging.access_format", "server.logs.access.format"},
		{"server.logging.level", "server.logs.level"},
		{"server.logging", "server.logs"},
		{"server.schedule", "server.scheduler"},
		{"server.web.cookie_consent.message", "server.privacy.consent.message"},
		{"server.web.cookie_consent.policy_url", "server.privacy.consent.policy.url"},
		{"server.web", "web"},
		{"server.security.hsts", "web.hsts"},
		{"server.security.nel", "web.headers.nel"},
		{"server.security.headers.clear_site_data", "web.headers.clear_site_data"},
		{"server.security.csrf", "server.csrf"},
		{"server.security_headers.hsts", "web.hsts.enabled"},
		{"server.security_headers.hsts_max_age", "web.hsts.max_age_seconds"},
		{"web.csrf", "server.csrf"},
		{"server.title", "server.branding.title"},
		{"server.description", "server.branding.description"},
		{"server.branding.logo_url", "server.branding.logo"},
		{"server.branding.favicon_url", "server.branding.favicon"},
		{"server.branding.primary_color", "web.ui.accent"},
		{"server.admin.email", "server.contact.admin.email"},
		{"server.ssl.dns01.provider", "server.ssl.letsencrypt.dns_provider"},
		{"server.ssl.dns01.credentials", "server.ssl.letsencrypt.dns_credentials"},
		{"server.ssl.letsencrypt.provider", "server.ssl.letsencrypt.dns_provider"},
		{"web.favicon_url", "server.branding.favicon"},
		{"web.ui.favicon", "server.branding.favicon"},
		{"web.ui.logo", "server.branding.logo"},
	} {
		if moveYAMLPath(root, move[0], move[1]) {
			changed = true
		}
	}
	for _, move := range [][2]string{
		{"server.seo.default_title", "server.branding.title"},
		{"server.seo.default_description", "server.branding.description"},
		{"server.seo.opengraph.image", "server.seo.og_image"},
		{"server.seo.twitter.site", "server.seo.twitter_handle"},
	} {
		if moveYAMLPathWhenEmpty(root, move[0], move[1]) {
			changed = true
		}
	}
	for _, page := range []string{"about", "privacy", "terms"} {
		if migrateLegacyPageContent(root, page) {
			changed = true
		}
	}
	if value := removeYAMLPathValue(root, "server.branding.footer_text"); value != nil {
		body := cloneYAMLNode(value)
		if body.Kind == yaml.ScalarNode {
			body.Value = "<p>" + html.EscapeString(body.Value) + "</p>"
		}
		setLegacyContent(root, "footer", "html", body)
		changed = true
	}
	if value := removeYAMLPathValue(root, "web.footer.custom_html"); value != nil {
		setLegacyContent(root, "footer", "html", value)
		changed = true
	}
	if migrateLegacyHTTPSPort(root) {
		changed = true
	}
	for _, oldPath := range []string{
		"server.branding.theme_color", "server.branding.source_code_url", "server.security_headers", "server.security.headers",
		"server.privacy.content", "server.mcp", "server.rate_limit.enabled", "server.rate_limit.requests",
		"server.rate_limit.window", "server.maintenance_mode", "server.secret_key", "web.permissions_policy",
		"web.honor_dnt", "web.robots.ai_bots", "server.web.cookie_consent.enabled",
		"server.admin.path", "server.admin.password", "server.admin.token", "server.admin.username", "server.admin.two_factor",
		"server.seo.enabled", "server.seo.sitemap.enabled", "server.seo.feed.enabled", "server.seo.opengraph.description",
		"server.ssl.letsencrypt.credentials_encrypted", "server.ssl.letsencrypt.validated_at", "server.ssl.letsencrypt.domain", "server.ssl.letsencrypt.domains",
		"server.ssl.dns01",
		"server.i18n.auto_detect", "server.i18n.rtl_languages", "server.i18n.show_selector", "server.i18n.translations_dir",
		"server.geoip.dir", "server.geoip.databases", "server.geoip.country_mode", "server.database.sqlite",
		"server.metrics.endpoint", "server.notifications.email_enabled", "server.notifications.webhook_enabled",
	} {
		if removeYAMLPath(root, oldPath) {
			changed = true
		}
	}
	if normalizeLegacyScheduler(root) {
		changed = true
	}
	return changed
}

func migrateLegacyDatabase(root *yaml.Node) bool {
	legacy := removeYAMLPathValue(root, "database")
	if legacy == nil {
		return false
	}
	if legacy.Kind != yaml.MappingNode {
		insertYAMLPath(root, strings.Split("database", "."), legacy)
		return false
	}
	destination := yamlPathValue(root, "server.database")
	if destination == nil {
		destination = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		insertYAMLPath(root, strings.Split("server.database", "."), destination)
		destination = yamlPathValue(root, "server.database")
	}
	for index := 0; index+1 < len(legacy.Content); index += 2 {
		key, value := legacy.Content[index], legacy.Content[index+1]
		if key.Value == "primary" && value.Kind == yaml.MappingNode {
			for childIndex := 0; childIndex+1 < len(value.Content); childIndex += 2 {
				if yamlMapValue(destination, value.Content[childIndex].Value) == nil {
					appendNodeMap(destination, value.Content[childIndex], value.Content[childIndex+1])
				}
			}
			continue
		}
		name := key.Value
		if name == "type" {
			name = "driver"
		}
		if yamlMapValue(destination, name) == nil {
			insertYAMLPath(destination, []string{name}, value)
		}
	}
	return true
}

func migrateLegacyHTTPSPort(root *yaml.Node) bool {
	value := removeYAMLPathValue(root, "server.https_port")
	if value == nil {
		return false
	}
	port, err := strconv.Atoi(strings.TrimSpace(value.Value))
	if err != nil || port <= 0 {
		return true
	}
	current := yamlPathValue(root, "server.port")
	if current == nil || strings.TrimSpace(current.Value) == "" {
		insertYAMLPath(root, strings.Split("server.port", "."), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strconv.Itoa(port)})
		return true
	}
	currentPort, err := strconv.Atoi(strings.TrimSpace(current.Value))
	if err != nil || currentPort <= 0 {
		return true
	}
	current.Value = strconv.Itoa(currentPort) + "," + strconv.Itoa(port)
	return true
}

func moveYAMLPathWhenEmpty(root *yaml.Node, from, to string) bool {
	value := removeYAMLPathValue(root, from)
	if value == nil {
		return false
	}
	if existing := yamlPathValue(root, to); existing != nil && strings.TrimSpace(existing.Value) != "" {
		return true
	}
	insertYAMLPath(root, strings.Split(to, "."), value)
	return true
}

func normalizeLegacyScheduler(root *yaml.Node) bool {
	scheduler := yamlPathValue(root, "server.scheduler")
	if scheduler == nil || scheduler.Kind != yaml.MappingNode {
		return false
	}
	changed := false
	for index := 0; index+1 < len(scheduler.Content); {
		key, value := scheduler.Content[index], scheduler.Content[index+1]
		if key.Value != "timezone" && key.Value != "catch_up_window" && key.Value != "tasks" {
			scheduler.Content = append(scheduler.Content[:index], scheduler.Content[index+2:]...)
			changed = true
			continue
		}
		index += 2
		if key.Value != "tasks" || value.Kind != yaml.MappingNode {
			continue
		}
		for taskIndex := 1; taskIndex < len(value.Content); taskIndex += 2 {
			task := value.Content[taskIndex]
			if task.Kind != yaml.MappingNode {
				continue
			}
			for fieldIndex := 0; fieldIndex+1 < len(task.Content); {
				field := task.Content[fieldIndex].Value
				allowed := field == "enabled" || field == "schedule" || field == "retry_on_fail" || field == "retry_delay" || (value.Content[taskIndex-1].Value == "backup" && field == "retention")
				if !allowed {
					task.Content = append(task.Content[:fieldIndex], task.Content[fieldIndex+2:]...)
					changed = true
					continue
				}
				fieldIndex += 2
			}
			var configured SchedulerTaskYAML
			if err := task.Decode(&configured); err == nil {
				if defaults, ok := Default("").Server.Scheduler.Tasks[value.Content[taskIndex-1].Value]; ok && reflect.DeepEqual(configured, defaults) {
					value.Content = append(value.Content[:taskIndex-1], value.Content[taskIndex+1:]...)
					taskIndex -= 2
					changed = true
				}
			}
		}
	}
	return changed
}

func migrateLegacyPageContent(root *yaml.Node, name string) bool {
	value := removeYAMLPathValue(root, "server.pages."+name+".content")
	if value == nil {
		return false
	}
	setLegacyContent(root, name, "md", value)
	return true
}

func setLegacyContent(root *yaml.Node, name, format string, body *yaml.Node) {
	content := yamlPathValue(root, "server.branding.content")
	if content == nil {
		content = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		insertYAMLPath(root, strings.Split("server.branding.content", "."), content)
		content = yamlPathValue(root, "server.branding.content")
	}
	for index := 0; index+1 < len(content.Content); index += 2 {
		if content.Content[index].Value == name {
			return
		}
	}
	page := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "format"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: format},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: "body"}, cloneYAMLNode(body),
	}}
	content.Content = append(content.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, page)
}

func moveYAMLPath(root *yaml.Node, from, to string) bool {
	value := removeYAMLPathValue(root, from)
	if value == nil {
		return false
	}
	if existing := yamlPathValue(root, to); existing != nil {
		return true
	}
	insertYAMLPath(root, strings.Split(to, "."), value)
	return true
}

func removeYAMLPath(root *yaml.Node, path string) bool {
	return removeYAMLPathValue(root, path) != nil
}

func removeYAMLPathValue(root *yaml.Node, path string) *yaml.Node {
	parts := strings.Split(path, ".")
	parent := root
	for _, part := range parts[:len(parts)-1] {
		parent = yamlMapValue(parent, part)
		if parent == nil {
			return nil
		}
	}
	for index := 0; index+1 < len(parent.Content); index += 2 {
		if parent.Content[index].Value == parts[len(parts)-1] {
			value := parent.Content[index+1]
			parent.Content = append(parent.Content[:index], parent.Content[index+2:]...)
			return value
		}
	}
	return nil
}

func yamlPathValue(root *yaml.Node, path string) *yaml.Node {
	current := root
	for _, part := range strings.Split(path, ".") {
		current = yamlMapEntry(current, part)
		if current == nil {
			return nil
		}
	}
	return current
}

func yamlMapEntry(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

// MigrateSecretsFiles moves supported credentials from server.yml and drop-ins
// into secrets.yml before the merged config is loaded.
func MigrateSecretsFiles(path string) error {
	dir := filepath.Dir(path)
	files := []string{path}
	dropDir := filepath.Join(dir, "yaml.d")
	entries, err := os.ReadDir(dropDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		files = append(files, filepath.Join(dropDir, name))
	}
	var sources []secretSource
	extracted := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, sourcePath := range files {
		data, readErr := os.ReadFile(sourcePath)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		doc, parseErr := parseConfigDocument(data)
		if parseErr != nil {
			if sourcePath != path {
				continue
			}
			return fmt.Errorf("config: parse %s for credential migration: %w", sourcePath, parseErr)
		}
		changed := extractSecretNodes(doc.Content[0], nil, extracted)
		if changed {
			sources = append(sources, secretSource{path: sourcePath, original: data, document: doc})
		}
	}
	if len(sources) == 0 {
		return nil
	}
	secretsPath := filepath.Join(dir, "secrets.yml")
	if existing, readErr := os.ReadFile(secretsPath); readErr == nil {
		doc, parseErr := parseConfigDocument(existing)
		if parseErr != nil {
			return fmt.Errorf("config: parse secrets.yml: %w", parseErr)
		}
		mergeYAMLNode(extracted, doc.Content[0])
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	secretBytes, err := encodeYAML(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{extracted}})
	if err != nil {
		return err
	}
	if err := writeAtomic(secretsPath, secretBytes, 0o600); err != nil {
		return err
	}
	for _, source := range sources {
		if source.path == path {
			if err := writeAtomic(path+".bak", source.original, 0o600); err != nil {
				return err
			}
		}
		remaining, err := encodeYAML(source.document)
		if err != nil {
			return err
		}
		if err := writeAtomic(source.path, remaining, 0o600); err != nil {
			return err
		}
	}
	return nil
}

type secretSource struct {
	path     string
	original []byte
	document *yaml.Node
}

func extractSecretNodes(mapping *yaml.Node, prefix []string, secrets *yaml.Node) bool {
	changed := false
	if mapping.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(mapping.Content); {
			key, value := mapping.Content[index], mapping.Content[index+1]
			path := append(append([]string(nil), prefix...), key.Value)
			if isSecretPath(path) || ((joinedPath(path) == "server.database.url" || joinedPath(path) == "server.cache.url") && value.Kind == yaml.ScalarNode && connectionHasCredentials(value.Value)) {
				insertYAMLPath(secrets, path, value)
				mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
				changed = true
				continue
			}
			if extractSecretNodes(value, path, secrets) {
				changed = true
			}
			index += 2
		}
	} else if mapping.Kind == yaml.SequenceNode {
		for index, child := range mapping.Content {
			if extractSecretNodes(child, append(append([]string(nil), prefix...), strconv.Itoa(index)), secrets) {
				changed = true
			}
		}
	}
	return changed
}

func isSecretPath(path []string) bool {
	joined := strings.Join(path, ".")
	for _, exact := range []string{
		"server.token", "server.security.encryption_key", "server.security.encryption_key_version",
		"server.notifications.email.smtp.password", "server.cache.password",
		"server.database.token", "server.pages.contact.captcha_secret_key", "server.ssl.letsencrypt.dns_credentials",
		"data.cve.api_key", "server.backup.encryption_password", "server.metrics.auth.tokens.prometheus",
		"server.metrics.auth.tokens.grafana", "server.metrics.auth.tokens.loki",
	} {
		if joined == exact {
			return true
		}
	}
	if len(path) >= 5 && path[0] == "server" && path[1] == "contact" {
		// Contact role webhook URLs and their secrets are credentials.
		if len(path) >= 5 && path[3] == "webhooks" {
			return true
		}
	}
	if len(path) == 6 && strings.Join(path[:4], ".") == "server.auth.ldap.providers" && isIndex(path[4]) && path[5] == "bind_password" {
		return true
	}
	if len(path) == 6 && strings.Join(path[:4], ".") == "server.auth.oidc.providers" && isIndex(path[4]) && path[5] == "client_secret" {
		return true
	}
	return false
}

func joinedPath(path []string) string { return strings.Join(path, ".") }

func insertYAMLPath(root *yaml.Node, path []string, value *yaml.Node) {
	current := root
	for _, part := range path[:len(path)-1] {
		if isIndex(part) {
			index, _ := strconv.Atoi(part)
			if current.Kind != yaml.SequenceNode {
				current.Kind, current.Tag, current.Content = yaml.SequenceNode, "!!seq", nil
			}
			for len(current.Content) <= index {
				current.Content = append(current.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
			}
			current = current.Content[index]
			continue
		}
		next := yamlMapValue(current, part)
		if next == nil {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			current.Content = append(current.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part}, next)
		}
		current = next
	}
	key := path[len(path)-1]
	if isIndex(key) {
		index, _ := strconv.Atoi(key)
		if current.Kind != yaml.SequenceNode {
			current.Kind, current.Tag, current.Content = yaml.SequenceNode, "!!seq", nil
		}
		for len(current.Content) <= index {
			current.Content = append(current.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
		}
		current.Content[index] = cloneYAMLNode(value)
		return
	}
	for index := 0; index+1 < len(current.Content); index += 2 {
		if current.Content[index].Value == key {
			current.Content[index+1] = cloneYAMLNode(value)
			return
		}
	}
	current.Content = append(current.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, cloneYAMLNode(value))
}

func isIndex(value string) bool {
	_, err := strconv.Atoi(value)
	return err == nil
}

func serverArea(area *yaml.Node) *yaml.Node {
	server := yamlMapValue(area, "server")
	if server == nil {
		server = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		area.Content = append(area.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "server"}, server)
	}
	return server
}

func webAreaMap(area *yaml.Node) *yaml.Node {
	web := yamlMapValue(area, "web")
	if web == nil {
		web = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		area.Content = append(area.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "web"}, web)
	}
	return web
}

func appendNodeMap(target, key, value *yaml.Node) {
	target.Content = append(target.Content, cloneYAMLNode(key), cloneYAMLNode(value))
}

func cloneYAMLNode(source *yaml.Node) *yaml.Node {
	copy := *source
	copy.Content = make([]*yaml.Node, len(source.Content))
	for index, child := range source.Content {
		copy.Content[index] = cloneYAMLNode(child)
	}
	return &copy
}

func mergeAreaFile(path string, moved *yaml.Node) ([]byte, error) {
	var root *yaml.Node
	if data, err := os.ReadFile(path); err == nil {
		doc, parseErr := parseConfigDocument(data)
		if parseErr != nil {
			return nil, fmt.Errorf("config: parse area file %s: %w", path, parseErr)
		}
		mergeYAMLNode(moved, doc.Content[0])
		root = moved
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		root = moved
	}
	return encodeYAML(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}})
}

func encodeYAML(document *yaml.Node) ([]byte, error) {
	var output strings.Builder
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return []byte(strings.TrimRight(output.String(), "\n") + "\n"), nil
}
