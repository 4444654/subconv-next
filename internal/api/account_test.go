package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"subconv-next/internal/authn"
	"subconv-next/internal/model"
)

func TestAccountLoginRejectsMissingOrIncorrectUsername(t *testing.T) {
	handler := newPublicLoginTestServer(t).Handler()
	for _, username := range []string{"", "other", "Admin"} {
		body, _ := json.Marshal(authLoginRequest{Username: username, Password: "a-strong-management-token"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body)))
		if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("username %q: status=%d cookies=%d", username, rec.Code, len(rec.Result().Cookies()))
		}
	}
	loginAccountForTest(t, handler, "203.0.113.80:1234", "admin", "a-strong-management-token")
}

func TestIndependentAccountPasswordAndAPIToken(t *testing.T) {
	const password = "  my independent password!  "
	hash, err := authn.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	cfg.Service.ManagementUsername = "operator"
	cfg.Service.ManagementPasswordHash = hash
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()
	for _, invalid := range []authLoginRequest{
		{Username: "admin", Password: password},
		{Username: "operator", Password: strings.TrimSpace(password)},
		{Username: "operator", Password: "a-strong-management-token"},
		{Username: "operator", Password: password + strings.Repeat("x", 73)},
	} {
		body, _ := json.Marshal(invalid)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body)))
		if rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
			t.Fatalf("invalid account received status=%d cookies=%d", rec.Code, len(rec.Result().Cookies()))
		}
	}
	cookie, csrf := loginAccountForTest(t, handler, "203.0.113.80:1234", "operator", password)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	if rec.Code != http.StatusOK || csrf == "" {
		t.Fatalf("account session rejected: %d", rec.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer a-strong-management-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, request)
	if rec.Code != http.StatusOK {
		t.Fatalf("existing API token rejected: %d", rec.Code)
	}

	// Changing only the account invalidates the old cookie, leaving API auth intact.
	cfg.Service.ManagementUsername = "new-operator"
	changed, _ := newTestServer(t, cfg)
	request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.AddCookie(cookie)
	rec = httptest.NewRecorder()
	changed.Handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old account session accepted: %d", rec.Code)
	}

	// Configured accounts also work without an API token.
	cfg.Service.AccessToken = ""
	tokenless, _ := newTestServer(t, cfg)
	cookie, _ = loginAccountForTest(t, tokenless.Handler(), "203.0.113.80:1234", "new-operator", password)
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	rec = httptest.NewRecorder()
	tokenless.Handler().ServeHTTP(rec, request)
	if rec.Code != http.StatusOK {
		t.Fatalf("account-only session rejected: %d", rec.Code)
	}
}

func TestPasswordHashIsNotExposedOrCopiedIntoWorkspaces(t *testing.T) {
	hash, err := authn.HashPassword("independent-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, public := range []bool{false, true} {
		cfg := model.DefaultConfig()
		cfg.Service.ManagementUsername = "operator"
		cfg.Service.ManagementPasswordHash = hash
		cfg.Service.PublicConverter = public
		server, _ := newTestServer(t, cfg)
		ref, err := server.createWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		workspace, err := server.loadWorkspaceConfig(ref)
		if err != nil {
			t.Fatal(err)
		}
		if workspace.Service.ManagementUsername != "" || workspace.Service.ManagementPasswordHash != "" {
			t.Fatal("workspace inherited daemon account credentials")
		}
		redacted, _ := json.Marshal(redactConfigForResponse(cfg, public))
		if strings.Contains(string(redacted), hash) || strings.Contains(string(redacted), "management_password_hash") {
			t.Fatal("configuration response exposed the password hash")
		}
	}
}
