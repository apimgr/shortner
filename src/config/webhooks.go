package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// EnsureWebhookSecrets creates a private signing key for every configured
// webhook that does not already have one. Returned names are safe for audit
// records; secret values are never included.
func EnsureWebhookSecrets(cfg *Config) ([]string, error) {
	generated := []string{}
	roles := map[string]*ContactRecipient{
		"admin":    &cfg.Server.Contact.Admin,
		"general":  &cfg.Server.Contact.General,
		"security": &cfg.Server.Contact.Security,
		"abuse":    &cfg.Server.Contact.Abuse,
	}
	for role, recipient := range roles {
		if recipient.Webhooks == nil {
			continue
		}
		for transport, endpoint := range recipient.Webhooks {
			if strings.HasSuffix(transport, "_secret") || strings.TrimSpace(endpoint) == "" {
				continue
			}
			secretName := transport + "_secret"
			if strings.TrimSpace(recipient.Webhooks[secretName]) != "" {
				continue
			}
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				return generated, fmt.Errorf("config: generate webhook signing secret: %w", err)
			}
			recipient.Webhooks[secretName] = hex.EncodeToString(secret)
			generated = append(generated, "server.contact."+role+".webhooks."+secretName)
		}
	}
	return generated, nil
}
