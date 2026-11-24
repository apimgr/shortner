package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/shortner/src/cache"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/mode"
)

func TestRedactDebugConfigHidesWebhookAndURLCredentials(t *testing.T) {
	databaseURL := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("dbuser", "DbPassword"),
		Host:     "db.example",
		Path:     "/app",
		RawQuery: url.Values{"token": {"QueryToken"}, "sslmode": {"require"}}.Encode(),
	}).String()
	config := map[string]any{
		"contact": map[string]any{
			"admin": map[string]any{
				"webhooks": map[string]any{
					"telegram":        "https://api.telegram.org/botBotSecret/sendMessage?chat_id=123",
					"telegram_secret": "SigningSecret",
				},
			},
		},
		"database": map[string]any{"url": databaseURL},
	}
	encoded, err := json.Marshal(redactDebugConfig(config))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"BotSecret", "SigningSecret", "dbuser", "DbPassword", "QueryToken"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("debug config exposed %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "[redacted]") || !strings.Contains(string(encoded), "sslmode=require") {
		t.Fatalf("debug config did not preserve safe URL data and redact credentials: %s", encoded)
	}
}

func TestDebugCacheReportsScopedStats(t *testing.T) {
	mode.SetDebugEnabled(true)
	defer mode.SetDebugEnabled(false)
	cfg := config.Default("")
	cfg.Server.Token = "adm_test"
	store := cache.NewMemory()
	store.Set(context.Background(), "key", "value", time.Minute)
	server := New(Options{Config: cfg, Cache: store})
	req := httptest.NewRequest(http.MethodGet, "/debug/cache", nil)
	req.Header.Set("Authorization", "Bearer adm_test")
	rec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(rec, req)
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || result["stats_available"] != true || result["entries"] != float64(1) || result["bytes"] != float64(len("keyvalue")) {
		t.Fatalf("debug cache = %d %v, want one scoped item and its byte count", rec.Code, result)
	}
}
