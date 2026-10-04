package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"subconv-next/internal/model"
)

func TestSecurityHeaders(t *testing.T) {
	server, _ := newTestServer(t, model.DefaultConfig())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusOK)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := rec.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if got := rec.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("Content-Security-Policy is empty")
	} else if !strings.Contains(got, "connect-src 'self'") || strings.Contains(got, "connect-src 'self' https:") {
		t.Fatalf("Content-Security-Policy allows external connections: %q", got)
	}
}

func TestForwardedProtoRequiresExplicitProxyTrust(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://subconv.example.com/healthz", nil)
	request.Header.Set("X-Forwarded-Proto", "https")

	untrustedServer, _ := newTestServer(t, model.DefaultConfig())
	untrustedRecorder := httptest.NewRecorder()
	untrustedServer.Handler().ServeHTTP(untrustedRecorder, request)
	if got := untrustedRecorder.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("untrusted forwarded proto set HSTS: %q", got)
	}
	request.Header.Set("Origin", "https://subconv.example.com")
	if sameOriginRequest(request, "", false) {
		t.Fatal("untrusted forwarded proto changed same-origin scheme")
	}

	trustedConfig := model.DefaultConfig()
	trustedConfig.Service.TrustProxyHeaders = true
	trustedServer, _ := newTestServer(t, trustedConfig)
	trustedRecorder := httptest.NewRecorder()
	trustedServer.Handler().ServeHTTP(trustedRecorder, request)
	if got := trustedRecorder.Header().Get("Strict-Transport-Security"); got == "" {
		t.Fatal("trusted HTTPS proxy request did not set HSTS")
	}
	if !sameOriginRequest(request, "", true) {
		t.Fatal("trusted forwarded proto was not used for same-origin scheme")
	}
}

func TestManagementLoginBehindTLSProxy(t *testing.T) {
	const token = "a-strong-management-token"
	for _, test := range []struct {
		name, publicURL, host, origin, fetchSite string
		wantStatus                               int
	}{
		{"missing public URL", "", "subconv.example.com", "https://subconv.example.com", "same-origin", http.StatusForbidden},
		{"configured HTTPS", "https://subconv.example.com", "subconv.example.com", "https://subconv.example.com", "same-origin", http.StatusOK},
		{"rewritten backend Host", "https://subconv.example.com", "127.0.0.1:9876", "https://subconv.example.com", "same-origin", http.StatusOK},
		{"explicit default port", "https://subconv.example.com", "subconv.example.com", "https://subconv.example.com:443", "same-origin", http.StatusOK},
		{"custom HTTPS port", "https://subconv.example.com:8443", "127.0.0.1:9876", "https://subconv.example.com:8443", "same-origin", http.StatusOK},
		{"incorrect port", "https://subconv.example.com:8443", "subconv.example.com", "https://subconv.example.com", "same-origin", http.StatusForbidden},
		{"incorrect scheme", "https://subconv.example.com", "subconv.example.com", "http://subconv.example.com", "same-origin", http.StatusForbidden},
		{"incorrect domain", "https://subconv.example.com", "subconv.example.com", "https://attacker.example", "same-origin", http.StatusForbidden},
		{"cross-site metadata", "https://subconv.example.com", "subconv.example.com", "https://subconv.example.com", "cross-site", http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := model.DefaultConfig()
			cfg.Service.AccessToken = token
			cfg.Service.PublicBaseURL = test.publicURL
			server, _ := newTestServer(t, cfg)
			handler := server.Handler()
			body, err := json.Marshal(authLoginRequest{Username: "admin", Password: token})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/api/auth/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", test.origin)
			req.Header.Set("Sec-Fetch-Site", test.fetchSite)
			req.Header.Set("X-Forwarded-Proto", "https")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("login status=%d, want %d; body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
			cookies := rec.Result().Cookies()
			if test.wantStatus != http.StatusOK {
				if len(cookies) != 0 || !strings.Contains(rec.Body.String(), "CROSS_ORIGIN_REQUEST") {
					t.Fatalf("rejected login cookies=%d body=%s", len(cookies), rec.Body.String())
				}
				return
			}
			if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly {
				t.Fatalf("HTTPS proxy login did not issue one Secure HttpOnly cookie: %+v", cookies)
			}
			var session authSessionResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
				t.Fatal(err)
			}
			if !session.Authenticated || session.CSRFToken == "" {
				t.Fatal("login did not create an authenticated CSRF session")
			}
			mutation := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/api/workspaces", nil)
			mutation.Header = req.Header.Clone()
			mutation.Header.Set(managementCSRFHeader, session.CSRFToken)
			mutation.AddCookie(cookies[0])
			mutationRec := httptest.NewRecorder()
			handler.ServeHTTP(mutationRec, mutation)
			if mutationRec.Code != http.StatusOK {
				t.Fatalf("authenticated mutation status=%d body=%s", mutationRec.Code, mutationRec.Body.String())
			}
		})
	}
}

func TestPublicManagementAPIRequiresAccessToken(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	req.RemoteAddr = "203.0.113.9:49152"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("WWW-Authenticate is empty")
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthReq.RemoteAddr = req.RemoteAddr
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status code = %d, want %d", healthRec.Code, http.StatusOK)
	}
}

func TestPublicManagementAPIAcceptsBearerToken(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	req.RemoteAddr = "203.0.113.10:49152"
	req.Header.Set("Authorization", "Bearer a-strong-management-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	queryReq := httptest.NewRequest(http.MethodPost, "/api/workspaces?token=a-strong-management-token", nil)
	queryReq.RemoteAddr = req.RemoteAddr
	queryRec := httptest.NewRecorder()
	handler.ServeHTTP(queryRec, queryReq)
	if queryRec.Code != http.StatusUnauthorized {
		t.Fatalf("query token status code = %d, want %d", queryRec.Code, http.StatusUnauthorized)
	}
}

func TestPublicWebUIAcceptsBasicToken(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	unauthorizedReq.RemoteAddr = "203.0.113.11:49152"
	unauthorizedRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedRec, unauthorizedReq)
	if unauthorizedRec.Code != http.StatusFound {
		t.Fatalf("unauthorized status code = %d, want %d", unauthorizedRec.Code, http.StatusFound)
	}
	if location := unauthorizedRec.Header().Get("Location"); !strings.HasPrefix(location, "/login?next=") {
		t.Fatalf("redirect location = %q, want login page", location)
	}

	authorizedReq := httptest.NewRequest(http.MethodGet, "/", nil)
	authorizedReq.RemoteAddr = unauthorizedReq.RemoteAddr
	authorizedReq.SetBasicAuth("subconv", "a-strong-management-token")
	authorizedRec := httptest.NewRecorder()
	handler.ServeHTTP(authorizedRec, authorizedReq)
	if authorizedRec.Code != http.StatusOK {
		t.Fatalf("authorized status code = %d, want %d; body=%s", authorizedRec.Code, http.StatusOK, authorizedRec.Body.String())
	}
}

func TestManagementLoginCreatesCookieSession(t *testing.T) {
	server := newPublicLoginTestServer(t)
	handler := server.Handler()

	loginPageReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginPageReq.RemoteAddr = "203.0.113.30:49152"
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)
	if loginPageRec.Code != http.StatusOK || !strings.Contains(loginPageRec.Body.String(), "登录管理后台") || !strings.Contains(loginPageRec.Body.String(), `autocomplete="username"`) {
		t.Fatalf("login page status/body = %d %q", loginPageRec.Code, loginPageRec.Body.String())
	}

	cookie, csrfToken := loginForTest(t, handler, "203.0.113.30:49152", "a-strong-management-token")
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("session cookie = %#v, want HttpOnly SameSite=Strict Path=/", cookie)
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootReq.RemoteAddr = "203.0.113.30:49152"
	rootReq.AddCookie(cookie)
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("authenticated root status = %d, want %d; body=%s", rootRec.Code, http.StatusOK, rootRec.Body.String())
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessionReq.RemoteAddr = rootReq.RemoteAddr
	sessionReq.AddCookie(cookie)
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, sessionReq)
	var session authSessionResponse
	if err := json.Unmarshal(sessionRec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if !session.Authenticated || session.CSRFToken != csrfToken || session.ExpiresAt == "" {
		t.Fatalf("session response = %#v, want authenticated session", session)
	}
}

func TestManagementSessionRequiresCSRFForMutation(t *testing.T) {
	server := newPublicLoginTestServer(t)
	handler := server.Handler()
	cookie, csrfToken := loginForTest(t, handler, "203.0.113.31:49152", "a-strong-management-token")

	withoutCSRF := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	withoutCSRF.RemoteAddr = "203.0.113.31:49152"
	withoutCSRF.AddCookie(cookie)
	withoutCSRFRec := httptest.NewRecorder()
	handler.ServeHTTP(withoutCSRFRec, withoutCSRF)
	if withoutCSRFRec.Code != http.StatusForbidden {
		t.Fatalf("without CSRF status = %d, want %d; body=%s", withoutCSRFRec.Code, http.StatusForbidden, withoutCSRFRec.Body.String())
	}

	withCSRF := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	withCSRF.RemoteAddr = withoutCSRF.RemoteAddr
	withCSRF.AddCookie(cookie)
	withCSRF.Header.Set(managementCSRFHeader, csrfToken)
	withCSRFRec := httptest.NewRecorder()
	handler.ServeHTTP(withCSRFRec, withCSRF)
	if withCSRFRec.Code != http.StatusOK {
		t.Fatalf("with CSRF status = %d, want %d; body=%s", withCSRFRec.Code, http.StatusOK, withCSRFRec.Body.String())
	}
}

func TestManagementLoginRejectsWrongPasswordAndTamperedSession(t *testing.T) {
	server := newPublicLoginTestServer(t)
	handler := server.Handler()

	wrongBody := bytes.NewBufferString(`{"password":"wrong-password"}`)
	wrongReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", wrongBody)
	wrongReq.RemoteAddr = "203.0.113.32:49152"
	wrongRec := httptest.NewRecorder()
	handler.ServeHTTP(wrongRec, wrongReq)
	if wrongRec.Code != http.StatusUnauthorized || len(wrongRec.Result().Cookies()) != 0 {
		t.Fatalf("wrong password response = %d cookies=%d", wrongRec.Code, len(wrongRec.Result().Cookies()))
	}

	cookie, _ := loginForTest(t, handler, wrongReq.RemoteAddr, "a-strong-management-token")
	cookie.Value += "tampered"
	apiReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	apiReq.RemoteAddr = wrongReq.RemoteAddr
	apiReq.AddCookie(cookie)
	apiRec := httptest.NewRecorder()
	handler.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered session status = %d, want %d", apiRec.Code, http.StatusUnauthorized)
	}
}

func TestManagementLogoutClearsSessionCookie(t *testing.T) {
	server := newPublicLoginTestServer(t)
	handler := server.Handler()
	cookie, csrfToken := loginForTest(t, handler, "203.0.113.33:49152", "a-strong-management-token")

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.RemoteAddr = "203.0.113.33:49152"
	logoutReq.AddCookie(cookie)
	logoutReq.Header.Set(managementCSRFHeader, csrfToken)
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d; body=%s", logoutRec.Code, http.StatusOK, logoutRec.Body.String())
	}
	cookies := logoutRec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != managementSessionCookie || cookies[0].MaxAge >= 0 {
		t.Fatalf("logout cookies = %#v, want expired management cookie", cookies)
	}
}

func TestConfiguredTokenRequiresLoginForPrivatePeer(t *testing.T) {
	server := newPublicLoginTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.RemoteAddr = "172.18.0.1:49152"
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("private peer status = %d, want %d when password configured", rec.Code, http.StatusUnauthorized)
	}
}

func newPublicLoginTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	server, _ := newTestServer(t, cfg)
	return server
}

func loginForTest(t *testing.T, handler http.Handler, remoteAddr, password string) (*http.Cookie, string) {
	t.Helper()
	return loginAccountForTest(t, handler, remoteAddr, "admin", password)
}

func loginAccountForTest(t *testing.T, handler http.Handler, remoteAddr, username, password string) (*http.Cookie, string) {
	t.Helper()
	body, err := json.Marshal(authLoginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatalf("marshal login request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response authSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want 1", len(cookies))
	}
	return cookies[0], response.CSRFToken
}

func TestPublicManagementAPIRejectsCrossOriginMutation(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	server, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	req.RemoteAddr = "203.0.113.12:49152"
	req.Header.Set("Authorization", "Bearer a-strong-management-token")
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestPublicAuthFailuresAreRateLimited(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()
	var lastCode int
	for i := 0; i < authFailureRateLimit+1; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.13:49152"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("last status code = %d, want %d", lastCode, http.StatusTooManyRequests)
	}
}

func TestPublicManagementAPIRejectsPrivatePeersWithoutToken(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()
	for _, remoteAddr := range []string{"127.0.0.1:49152", "172.18.0.1:49152"} {
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("remote %s status code = %d, want %d; body=%s", remoteAddr, rec.Code, http.StatusUnauthorized, rec.Body.String())
		}
	}
}

func TestPublicConverterStatelessGenerateDoesNotInheritServerRules(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	cfg.Render.RuleProviders = []model.RuleProviderConfig{{
		Name:     "server-private-provider",
		Type:     "http",
		URL:      "https://rules.example/private.yaml?token=server-secret",
		Behavior: "classical",
		Policy:   "DIRECT",
		Enabled:  true,
		Headers:  map[string][]string{"Authorization": {"Bearer server-header-secret"}},
	}}
	server, _ := newTestServer(t, cfg)
	body, err := json.Marshal(generateRequest{
		Template: "lite",
		Nodes: []model.NodeIR{{
			Name: "public-node", Type: "ss", Server: "example.com", Port: 443,
			Auth: model.Auth{Password: "pass"}, Raw: map[string]any{"method": "aes-256-gcm"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/generate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	for _, leaked := range []string{"server-private-provider", "server-secret", "server-header-secret"} {
		if strings.Contains(rec.Body.String(), leaked) {
			t.Fatalf("public generate response leaked server configuration %q: %s", leaked, rec.Body.String())
		}
	}
}

func TestExplicitInsecurePublicModeDisablesLogin(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AllowInsecurePublic = true
	server, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.20:49152"
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestPublicConverterModeAllowsAnonymousWorkspaceUI(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()

	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootReq.RemoteAddr = "203.0.113.40:49152"
	rootRec := httptest.NewRecorder()
	handler.ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusOK {
		t.Fatalf("anonymous root status = %d; body=%s", rootRec.Code, rootRec.Body.String())
	}

	workspaceReq := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
	workspaceReq.RemoteAddr = rootReq.RemoteAddr
	workspaceRec := httptest.NewRecorder()
	handler.ServeHTTP(workspaceRec, workspaceReq)
	if workspaceRec.Code != http.StatusOK {
		t.Fatalf("anonymous workspace status = %d; body=%s", workspaceRec.Code, workspaceRec.Body.String())
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessionReq.RemoteAddr = rootReq.RemoteAddr
	sessionRec := httptest.NewRecorder()
	handler.ServeHTTP(sessionRec, sessionReq)
	var session authSessionResponse
	if err := json.Unmarshal(sessionRec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode public session response: %v", err)
	}
	if session.Required || !session.Authenticated || session.Configured || !session.PublicConverter {
		t.Fatalf("public session response = %#v, want no login requirement or management-token disclosure", session)
	}

	loginPageReq := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginPageReq.RemoteAddr = rootReq.RemoteAddr
	loginPageRec := httptest.NewRecorder()
	handler.ServeHTTP(loginPageRec, loginPageReq)
	if loginPageRec.Code != http.StatusFound || loginPageRec.Header().Get("Location") != "/" {
		t.Fatalf("public login page response = %d location=%q, want redirect to root", loginPageRec.Code, loginPageRec.Header().Get("Location"))
	}

	loginAPIReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(`{"password":"guess"}`))
	loginAPIReq.RemoteAddr = rootReq.RemoteAddr
	loginAPIRec := httptest.NewRecorder()
	handler.ServeHTTP(loginAPIRec, loginAPIReq)
	if loginAPIRec.Code != http.StatusNotFound {
		t.Fatalf("public login API status = %d, want %d", loginAPIRec.Code, http.StatusNotFound)
	}

	protectedReq := httptest.NewRequest(http.MethodGet, "/api/unknown-management-route", nil)
	protectedReq.RemoteAddr = rootReq.RemoteAddr
	protectedRec := httptest.NewRecorder()
	handler.ServeHTTP(protectedRec, protectedReq)
	if protectedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown management route status = %d, want %d", protectedRec.Code, http.StatusUnauthorized)
	}
}

func TestPublicConverterDoesNotUsePublishedLinksForConfigRecovery(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.PublicConverter = true
	server, cfg := newTestServer(t, cfg)
	workspaceID := createWorkspaceForTest(t, server, cfg)

	restoreReq := httptest.NewRequest(
		http.MethodPost,
		withWorkspace("/api/workspaces/"+workspaceID+"/restore-from-published", workspaceID),
		bytes.NewBufferString(`{"url":"https://public.example/s/token/mihomo.yaml"}`),
	)
	restoreReq.Header.Set("Content-Type", "application/json")
	restoreRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusNotFound {
		t.Fatalf("public restore status = %d, want %d; body=%s", restoreRec.Code, http.StatusNotFound, restoreRec.Body.String())
	}

	configReq := httptest.NewRequest(http.MethodGet, withWorkspace("/api/config", workspaceID), nil)
	configRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(configRec, configReq)
	if configRec.Code != http.StatusOK {
		t.Fatalf("public config status = %d; body=%s", configRec.Code, configRec.Body.String())
	}
	var configBody configResponse
	if err := json.Unmarshal(configRec.Body.Bytes(), &configBody); err != nil {
		t.Fatalf("decode public config response: %v", err)
	}
	if configBody.Config.Service.OutputPath != "" || configBody.Config.Service.StatePath != "" || configBody.Config.Service.CacheDir != "" || configBody.Config.Service.ListenAddr != "" || configBody.Config.Service.ListenPort != 0 {
		t.Fatalf("public config leaked runtime paths/listener: %+v", configBody.Config.Service)
	}

	statusReq := httptest.NewRequest(http.MethodGet, withWorkspace("/api/status", workspaceID), nil)
	statusRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("public status = %d; body=%s", statusRec.Code, statusRec.Body.String())
	}
	var statusBody statusResponse
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusBody); err != nil {
		t.Fatalf("decode public status response: %v", err)
	}
	if statusBody.OutputPath != "" {
		t.Fatalf("public status leaked output path: %q", statusBody.OutputPath)
	}
}

func TestPublicConverterBoundsPublishedStorage(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	server.maxPublications = 1
	if _, err := server.createPublished("workspace-a"); err != nil {
		t.Fatalf("first public publication: %v", err)
	}
	if _, err := server.createPublished("workspace-b"); !errors.Is(err, errPublishedLimitReached) {
		t.Fatalf("second public publication error = %v, want errPublishedLimitReached", err)
	}
}

func TestPublicConverterRateLimitsWorkspaceCreation(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()
	remoteAddr := "203.0.113.41:49152"
	lastCode := 0
	for i := 0; i < anonymousWorkspaceRateLimit+1; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/workspaces", nil)
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("last workspace creation status = %d, want %d", lastCode, http.StatusTooManyRequests)
	}
}

func TestPublishedSubscriptionsAreRateLimited(t *testing.T) {
	server, _ := newTestServer(t, model.DefaultConfig())
	handler := server.Handler()
	lastCode := 0
	for i := 0; i < publishedSubscriptionRateLimit+1; i++ {
		req := httptest.NewRequest(http.MethodGet, "/s/not-a-valid-token/mihomo.yaml", nil)
		req.RemoteAddr = "203.0.113.71:49152"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		lastCode = rec.Code
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("last status code = %d, want %d", lastCode, http.StatusTooManyRequests)
	}
}

func TestPublicConverterDisablesPrivateSubscriptionAccess(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)

	privateService := model.DefaultConfig()
	privateService.Service.AllowLAN = true
	if err := server.validatePublicConverterConfig(privateService); err == nil {
		t.Fatal("public converter accepted service.allow_lan=true")
	}
	privateSubscription := model.DefaultConfig()
	privateSubscription.Subscriptions = []model.SubscriptionConfig{{AllowLAN: true}}
	if err := server.validatePublicConverterConfig(privateSubscription); err == nil {
		t.Fatal("public converter accepted subscription.allow_lan=true")
	}
}

func TestPublicWorkspacePolicyOwnsResourceLimits(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	cfg.Service.AccessToken = "a-strong-management-token"
	cfg.Service.MaxSubscriptionBytes = 8 * 1024 * 1024
	cfg.Service.FetchTimeoutSeconds = 120
	cfg.Service.RefreshInterval = 1
	server, _ := newTestServer(t, cfg)
	ref, err := server.createWorkspace()
	if err != nil {
		t.Fatalf("createWorkspace() error = %v", err)
	}
	workspaceCfg, err := server.loadWorkspaceConfig(ref)
	if err != nil {
		t.Fatalf("loadWorkspaceConfig() error = %v", err)
	}
	if workspaceCfg.Service.MaxSubscriptionBytes != publicMaxSubscriptionBytes ||
		workspaceCfg.Service.FetchTimeoutSeconds != publicMaxFetchTimeout ||
		workspaceCfg.Service.RefreshInterval != publicMinRefreshInterval {
		t.Fatalf("public workspace resource policy = %+v", workspaceCfg.Service)
	}
	if workspaceCfg.Service.AccessToken != "" || workspaceCfg.Service.SubscriptionToken != "" || !workspaceCfg.Service.PublicConverter {
		t.Fatalf("public workspace credentials/mode = %+v", workspaceCfg.Service)
	}
}

func TestPublicConverterRejectsOversizedConfiguration(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	candidate := model.DefaultConfig()
	for i := 0; i < publicMaxSubscriptions+1; i++ {
		candidate.Subscriptions = append(candidate.Subscriptions, model.SubscriptionConfig{
			Name: "source",
			URL:  "https://example.com/sub",
		})
	}
	if err := server.validatePublicConverterConfig(candidate); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("validatePublicConverterConfig() error = %v, want source limit", err)
	}
}

func TestPublicConverterRejectsRemoteCustomRuleSnapshots(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	candidate := model.DefaultConfig()
	candidate.Render.CustomRules = []model.CustomRule{{
		Enabled:    true,
		SourceType: "http",
		URL:        "https://rules.example.com/list.txt",
	}}
	if err := server.validatePublicConverterConfig(candidate); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("validatePublicConverterConfig() error = %v, want remote custom rule rejection", err)
	}
}

func TestPublicConverterLimitsStatelessWork(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	handler := server.Handler()

	parseBody, err := json.Marshal(parseRequest{Content: strings.Repeat("x", publicMaxParseContent+1)})
	if err != nil {
		t.Fatal(err)
	}
	parseReq := httptest.NewRequest(http.MethodPost, "/api/parse", bytes.NewReader(parseBody))
	parseRec := httptest.NewRecorder()
	handler.ServeHTTP(parseRec, parseReq)
	if parseRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized parse status = %d, want %d", parseRec.Code, http.StatusRequestEntityTooLarge)
	}

	generateBody, err := json.Marshal(generateRequest{Nodes: make([]model.NodeIR, publicMaxGenerateNodes+1)})
	if err != nil {
		t.Fatal(err)
	}
	generateReq := httptest.NewRequest(http.MethodPost, "/api/generate", bytes.NewReader(generateBody))
	generateRec := httptest.NewRecorder()
	handler.ServeHTTP(generateRec, generateReq)
	if generateRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized generate status = %d, want %d", generateRec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestPublicConverterRejectsCrossOriginRead(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/api/site-logo?url=https://example.com", nil)
	req.RemoteAddr = "203.0.113.43:49152"
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin read status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestPublicHealthDoesNotExposeRuntimePaths(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "data_dir") || strings.Contains(body, "version") {
		t.Fatalf("public health leaked runtime metadata: %s", body)
	}
}

func TestPublicManagementHealthRequiresAuthorizationForRuntimeDetails(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.AccessToken = "a-strong-management-token"
	server, _ := newTestServer(t, cfg)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("anonymous health status = %d", recorder.Code)
	}
	if body := recorder.Body.String(); strings.Contains(body, "data_dir") || strings.Contains(body, "version") {
		t.Fatalf("anonymous public health leaked runtime metadata: %s", body)
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer a-strong-management-token")
	authorizedRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(authorizedRecorder, authorizedRequest)
	if body := authorizedRecorder.Body.String(); !strings.Contains(body, "data_dir") || !strings.Contains(body, "version") {
		t.Fatalf("authorized health omitted runtime metadata: %s", body)
	}
}

func TestRecoveryMiddlewareReturnsStableError(t *testing.T) {
	server, _ := newTestServer(t, model.DefaultConfig())
	handler := server.recoveryMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("sensitive panic value")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "sensitive panic value") {
		t.Fatalf("panic response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestTrustedProxyClientAddressIsUsedForRateLimits(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.TrustProxyHeaders = true
	server, _ := newTestServer(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "172.18.0.1:49152"
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 172.18.0.2")
	if got := server.clientHost(req); got != "203.0.113.42" {
		t.Fatalf("clientHost() = %q, want forwarded client address", got)
	}
}

func TestRequestRateLimiter(t *testing.T) {
	limiter := newRequestRateLimiter()
	now := time.Now()
	if !limiter.allow("client", 2, now) || !limiter.allow("client", 2, now.Add(time.Second)) {
		t.Fatal("initial requests should be allowed")
	}
	if limiter.allow("client", 2, now.Add(2*time.Second)) {
		t.Fatal("request over the limit should be rejected")
	}
	if !limiter.allow("client", 2, now.Add(managementRateWindow+time.Second)) {
		t.Fatal("request after the rate window should be allowed")
	}
}

func TestPublicConverterRejectsInsecureSkipVerify(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.ListenAddr = "0.0.0.0"
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)

	insecure := model.DefaultConfig()
	insecure.Subscriptions = []model.SubscriptionConfig{
		{Name: "insecure", URL: "https://example.com/sub", InsecureSkipVerify: true},
	}
	if err := server.validatePublicConverterConfig(insecure); err == nil {
		t.Fatal("public converter accepted subscription.insecure_skip_verify=true")
	}
}

func TestPublicWorkspacePolicyForcesCertificateVerification(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	server, _ := newTestServer(t, cfg)
	ref, err := server.createWorkspace()
	if err != nil {
		t.Fatalf("createWorkspace() error = %v", err)
	}

	stale := model.DefaultConfig()
	stale.Subscriptions = []model.SubscriptionConfig{
		{Name: "stale", URL: "https://example.com/sub", AllowLAN: true, InsecureSkipVerify: true},
	}
	server.applyWorkspaceConfigPolicy(&stale, ref)
	if stale.Subscriptions[0].InsecureSkipVerify || stale.Subscriptions[0].AllowLAN {
		t.Fatalf("public policy kept unsafe subscription flags: %+v", stale.Subscriptions[0])
	}
}

func TestServerResourceLimitsFollowConfiguration(t *testing.T) {
	cfg := model.DefaultConfig()
	cfg.Service.PublicConverter = true
	cfg.Service.MaxWorkspaces = 1
	cfg.Service.MaxPublications = 1
	server, _ := newTestServer(t, cfg)

	if _, err := server.createWorkspace(); err != nil {
		t.Fatalf("first createWorkspace() error = %v", err)
	}
	if _, err := server.createWorkspace(); !errors.Is(err, errWorkspaceLimitReached) {
		t.Fatalf("second createWorkspace() error = %v, want errWorkspaceLimitReached", err)
	}

	if _, err := server.createPublished("w1"); err != nil {
		t.Fatalf("first createPublished() error = %v", err)
	}
	if _, err := server.createPublished("w2"); !errors.Is(err, errPublishedLimitReached) {
		t.Fatalf("second createPublished() error = %v, want errPublishedLimitReached", err)
	}
}
