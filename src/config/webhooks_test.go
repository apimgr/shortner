package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWebhookSecretsAndSaveSeparately(t *testing.T) {
	cfg := Default("")
	endpoint := "https://discord.example/webhook?token=endpoint-secret"
	cfg.Server.Contact.Admin.Webhooks = map[string]string{"discord": endpoint}
	generated, err := EnsureWebhookSecrets(cfg)
	if err != nil {
		t.Fatal(err)
	}
	secret := cfg.Server.Contact.Admin.Webhooks["discord_secret"]
	if len(generated) != 1 || generated[0] != "server.contact.admin.webhooks.discord_secret" || len(secret) != 64 {
		t.Fatalf("generated=%v secret length=%d", generated, len(secret))
	}
	path := filepath.Join(t.TempDir(), "server.yml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	serverYAML, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serverYAML), secret) || strings.Contains(string(serverYAML), endpoint) {
		t.Fatal("webhook credential was written to server.yml")
	}
	secretYAML, err := os.ReadFile(filepath.Join(filepath.Dir(path), "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(secretYAML), secret) || !strings.Contains(string(secretYAML), endpoint) {
		t.Fatal("webhook credentials were not written to secrets.yml")
	}
	loaded, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Server.Contact.Admin.Webhooks["discord_secret"]; got != secret {
		t.Fatalf("reloaded secret = %q, want generated value", got)
	}
	cfg.Server.Contact.Admin.Webhooks = nil
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	secretYAML, err = os.ReadFile(filepath.Join(filepath.Dir(path), "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secretYAML), secret) || strings.Contains(string(secretYAML), endpoint) {
		t.Fatal("removed webhook credentials remain in secrets.yml")
	}
	loaded, err = Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Server.Contact.Admin.Webhooks) != 0 {
		t.Fatalf("reloaded removed webhook credentials = %#v", loaded.Server.Contact.Admin.Webhooks)
	}
}
