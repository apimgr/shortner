package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/tor"
	"github.com/go-chi/chi/v5"
)

func TestTorControlRoutesLoopbackOnlyAndInternal(t *testing.T) {
	dirs := tor.Dirs{Config: t.TempDir(), Data: t.TempDir(), Log: t.TempDir()}
	manager := tor.NewManager(context.Background(), config.DefaultTor(), dirs, nil, nil)
	router := chi.NewRouter()
	registerTorControlRoutes(router, manager, config.DefaultTor(), dirs)

	remote := httptest.NewRequest(http.MethodGet, "/server/tor/status", nil)
	remote.RemoteAddr = "192.0.2.4:52300"
	remoteResult := httptest.NewRecorder()
	router.ServeHTTP(remoteResult, remote)
	if remoteResult.Code != http.StatusNotFound {
		t.Fatalf("non-loopback status = %d, want 404", remoteResult.Code)
	}

	local := httptest.NewRequest(http.MethodGet, "/server/tor/status", nil)
	local.RemoteAddr = "[::1]:52300"
	localResult := httptest.NewRecorder()
	router.ServeHTTP(localResult, local)
	if localResult.Code != http.StatusOK {
		t.Fatalf("loopback status = %d, want 200: %s", localResult.Code, localResult.Body.String())
	}
	var envelope struct {
		OK   bool                       `json:"ok"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(localResult.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Data["running"] == nil || envelope.Data["vanity"] == nil {
		t.Fatalf("unexpected status envelope: %s", localResult.Body.String())
	}
}

func TestTorControlRoutesRejectMethodMismatchAndUnknownJSON(t *testing.T) {
	dirs := tor.Dirs{Config: t.TempDir(), Data: t.TempDir(), Log: t.TempDir()}
	manager := tor.NewManager(context.Background(), config.DefaultTor(), dirs, nil, nil)
	router := chi.NewRouter()
	registerTorControlRoutes(router, manager, config.DefaultTor(), dirs)

	method := httptest.NewRequest(http.MethodDelete, "/server/tor/status", nil)
	method.RemoteAddr = "127.0.0.1:52300"
	methodResult := httptest.NewRecorder()
	router.ServeHTTP(methodResult, method)
	if methodResult.Code != http.StatusNotFound {
		t.Fatalf("method mismatch = %d, want 404", methodResult.Code)
	}

	unknown := httptest.NewRequest(http.MethodPost, "/server/tor/regenerate", strings.NewReader(`{"confirm":true,"unexpected":1}`))
	unknown.RemoteAddr = "127.0.0.1:52300"
	unknown.Header.Set("Content-Type", "application/json")
	unknownResult := httptest.NewRecorder()
	router.ServeHTTP(unknownResult, unknown)
	if unknownResult.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400: %s", unknownResult.Code, unknownResult.Body.String())
	}
}
