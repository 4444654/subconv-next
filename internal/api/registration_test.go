package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"subconv-next/internal/model"
)

func signupServerForTest(t *testing.T) *Server {
	t.Helper()
	cfg := model.DefaultConfig()
	cfg.Service.AccessToken = "a-strong-management-token"
	server, _ := newTestServer(t, cfg)
	return server
}

func accountRequestForTest(t *testing.T, handler http.Handler, method, path string, payload any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set(managementCSRFHeader, csrf)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func registerAccountForTest(t *testing.T, handler http.Handler, username string) (*http.Cookie, authSessionResponse) {
	t.Helper()
	rec := accountRequestForTest(t, handler, http.MethodPost, "/api/auth/register", authRegisterRequest{Username: username, Password: "  user password!  ", ConfirmPassword: "  user password!  "}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	var session authSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge != 12*60*60 || session.Role != "user" || session.UserID == "" || !session.Authenticated || session.CSRFToken == "" {
		t.Fatalf("registration session=%+v cookies=%+v", session, cookies)
	}
	return cookies[0], session
}

func accountWorkspaceForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, session authSessionResponse) string {
	t.Helper()
	rec := accountRequestForTest(t, handler, http.MethodPost, "/api/workspaces", nil, cookie, session.CSRFToken)
	var workspace workspaceResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &workspace) != nil || workspace.WorkspaceID == "" {
		t.Fatalf("workspace status=%d body=%s", rec.Code, rec.Body.String())
	}
	return workspace.WorkspaceID
}

func TestRegistrationPersistsAndLogsInAfterRestart(t *testing.T) {
	server := signupServerForTest(t)
	handler := server.Handler()
	cookie, session := registerAccountForTest(t, handler, "Alice")
	path := filepath.Join(server.baseDataDir(), "accounts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || bytes.Contains(data, []byte("user password")) || !bytes.Contains(data, []byte("password_hash")) {
		t.Fatal("account storage did not protect the password")
	}
	if _, _, ok := server.managementSession(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Fatal("unexpected admin session")
	}
	userRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	userRequest.AddCookie(cookie)
	if _, _, ok := server.managementSession(userRequest); ok {
		t.Fatal("registered cookie was accepted as an admin session")
	}
	restarted := NewServer("test", server.snapshotConfig()).Handler()
	rec := accountRequestForTest(t, restarted, http.MethodGet, "/api/auth/session", nil, cookie, "")
	var persisted authSessionResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &persisted) != nil || persisted.UserID != session.UserID || persisted.Role != "user" || !persisted.Authenticated {
		t.Fatalf("restart session status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = accountRequestForTest(t, restarted, http.MethodPost, "/api/auth/login", authLoginRequest{Username: "alice", Password: "  user password!  "}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login after restart status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = accountRequestForTest(t, restarted, http.MethodPost, "/api/auth/login", authLoginRequest{Username: "alice", Password: "user password!"}, nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("trimmed password status=%d", rec.Code)
	}
	loginForTest(t, restarted, "203.0.113.80:1234", "a-strong-management-token")
	duplicate := accountRequestForTest(t, restarted, http.MethodPost, "/api/auth/register", authRegisterRequest{Username: "ALICE", Password: "password123", ConfirmPassword: "password123"}, nil, "")
	if duplicate.Code != http.StatusConflict || len(duplicate.Result().Cookies()) != 0 {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
}

func TestRegistrationValidationAndDisabledMode(t *testing.T) {
	for _, test := range []struct {
		name    string
		request any
		status  int
	}{
		{"short username", authRegisterRequest{"ab", "password123", "password123"}, http.StatusBadRequest},
		{"invalid username", authRegisterRequest{"../alice", "password123", "password123"}, http.StatusBadRequest},
		{"admin reserved", authRegisterRequest{"Admin", "password123", "password123"}, http.StatusConflict},
		{"short password", authRegisterRequest{"alice", "short", "short"}, http.StatusBadRequest},
		{"long UTF8 password", authRegisterRequest{"alice", strings.Repeat("密", 25), strings.Repeat("密", 25)}, http.StatusBadRequest},
		{"confirmation required", authRegisterRequest{"alice", "password123", ""}, http.StatusBadRequest},
		{"confirmation mismatch", authRegisterRequest{"alice", "password123", "password124"}, http.StatusBadRequest},
		{"role injection", map[string]string{"username": "alice", "password": "password123", "confirm_password": "password123", "role": "admin"}, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := signupServerForTest(t)
			rec := accountRequestForTest(t, server.Handler(), http.MethodPost, "/api/auth/register", test.request, nil, "")
			if rec.Code != test.status || len(rec.Result().Cookies()) != 0 {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if _, err := os.Stat(filepath.Join(server.baseDataDir(), "accounts.json")); !os.IsNotExist(err) {
				t.Fatal("invalid registration wrote an account store")
			}
		})
	}
	server := signupServerForTest(t)
	handler := server.Handler()
	registerAccountForTest(t, handler, "alice")
	cfg := server.snapshotConfig()
	cfg.Service.RegistrationEnabled = false
	closed := NewServer("test", cfg).Handler()
	rec := accountRequestForTest(t, closed, http.MethodPost, "/api/auth/register", authRegisterRequest{"bob", "password123", "password123"}, nil, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled registration status=%d", rec.Code)
	}
	rec = accountRequestForTest(t, closed, http.MethodPost, "/api/auth/login", authLoginRequest{"alice", "  user password!  "}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("existing login after closing registration status=%d", rec.Code)
	}
	cfg.Service.PublicConverter = true
	public := NewServer("test", cfg).Handler()
	rec = accountRequestForTest(t, public, http.MethodPost, "/api/auth/register", authRegisterRequest{"bob", "password123", "password123"}, nil, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous mode registration status=%d", rec.Code)
	}
}

func TestRegisteredAccountsCannotAccessOtherWorkspacesOrPublications(t *testing.T) {
	server := signupServerForTest(t)
	handler := server.Handler()
	aliceCookie, alice := registerAccountForTest(t, handler, "alice")
	bobCookie, bob := registerAccountForTest(t, handler, "bob")
	aliceID := accountWorkspaceForTest(t, handler, aliceCookie, alice)
	bobID := accountWorkspaceForTest(t, handler, bobCookie, bob)
	ref, err := server.loadWorkspace(aliceID)
	if err != nil || ref.Meta.OwnerID != alice.UserID {
		t.Fatal("workspace did not belong to its registered account")
	}
	for _, test := range []struct{ method, path string }{
		{http.MethodGet, "/api/config?workspace=" + aliceID},
		{http.MethodPut, "/api/config?workspace=" + aliceID},
		{http.MethodGet, "/api/nodes?workspace=" + aliceID},
		{http.MethodGet, "/api/logs?workspace=" + aliceID},
		{http.MethodDelete, "/api/workspaces/" + aliceID},
		{http.MethodPost, "/api/workspaces/" + aliceID + "/restore-draft"},
	} {
		rec := accountRequestForTest(t, handler, test.method, test.path, nil, bobCookie, bob.CSRFToken)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("foreign workspace %s %s status=%d body=%s", test.method, test.path, rec.Code, rec.Body.String())
		}
	}
	rec := accountRequestForTest(t, handler, http.MethodGet, "/api/config?workspace="+aliceID, nil, aliceCookie, "")
	if rec.Code != http.StatusOK || bytes.Contains(rec.Body.Bytes(), []byte(server.baseDataDir())) || bytes.Contains(rec.Body.Bytes(), []byte("a-strong-management-token")) {
		t.Fatalf("own config status=%d body=%s", rec.Code, rec.Body.String())
	}
	noCSRF := accountRequestForTest(t, handler, http.MethodDelete, "/api/workspaces/"+aliceID, nil, aliceCookie, "")
	if noCSRF.Code != http.StatusForbidden {
		t.Fatalf("mutation without CSRF status=%d", noCSRF.Code)
	}
	published, err := server.createPublished(ref.Hash)
	if err != nil || published.Meta.OwnerID != alice.UserID {
		t.Fatal("publication did not belong to its registered account")
	}
	if err := os.WriteFile(published.CurrentPath, []byte("proxies: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = accountRequestForTest(t, handler, http.MethodPost, "/api/workspaces/"+bobID+"/bind-publish", bindPublishedRequest{PublishID: published.ID}, bobCookie, bob.CSRFToken)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign publication binding status=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = accountRequestForTest(t, handler, http.MethodPost, "/api/workspaces/"+aliceID+"/bind-publish", bindPublishedRequest{PublishID: published.ID}, aliceCookie, alice.CSRFToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("own publication binding status=%d body=%s", rec.Code, rec.Body.String())
	}
	for _, route := range []string{"/api/published/" + published.ID + "?workspace=" + bobID, "/api/workspaces/" + bobID + "/restore-from-published"} {
		rec = accountRequestForTest(t, handler, http.MethodGet, route, nil, bobCookie, bob.CSRFToken)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("foreign publication route status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	unsafe := model.DefaultConfig()
	unsafe.Subscriptions = []model.SubscriptionConfig{{URL: "http://127.0.0.1:9876/healthz", Enabled: true}}
	rec = accountRequestForTest(t, handler, http.MethodPut, "/api/config?workspace="+aliceID, unsafe, aliceCookie, alice.CSRFToken)
	if rec.Code != http.StatusBadRequest || !bytes.Contains(rec.Body.Bytes(), []byte("PUBLIC_CONFIG_REJECTED")) {
		t.Fatalf("registered private-network config status=%d body=%s", rec.Code, rec.Body.String())
	}
	adminRef, err := server.createWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	rec = accountRequestForTest(t, handler, http.MethodGet, "/api/config?workspace="+adminRef.ID, nil, aliceCookie, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("registered access to admin workspace status=%d", rec.Code)
	}
}

func TestRegistrationRejectsCrossOriginAndLimitsAttempts(t *testing.T) {
	server := signupServerForTest(t)
	handler := server.Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"username":"alice","password":"password123","confirm_password":"password123"}`))
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin registration=%d", rec.Code)
	}
	for i := 0; i <= anonymousWorkspaceRateLimit; i++ {
		rec = accountRequestForTest(t, handler, http.MethodPost, "/api/auth/register", authRegisterRequest{"ab", "password123", "password123"}, nil, "")
		if i == anonymousWorkspaceRateLimit && (rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "") {
			t.Fatalf("register rate limit status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestConcurrentRegistrationAndCorruptStore(t *testing.T) {
	server := signupServerForTest(t)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := server.createAccount("alice", "password123")
			results <- err
		}()
	}
	group.Wait()
	close(results)
	success, duplicate := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if err == errAccountExists {
			duplicate++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || duplicate != 1 {
		t.Fatalf("success=%d duplicate=%d", success, duplicate)
	}
	path := filepath.Join(server.baseDataDir(), "accounts.json")
	if err := os.WriteFile(path, []byte("broken registry"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := NewServer("test", server.snapshotConfig()).Handler()
	rec := accountRequestForTest(t, restarted, http.MethodPost, "/api/auth/register", authRegisterRequest{"bob", "password123", "password123"}, nil, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt registry register=%d", rec.Code)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken registry" {
		t.Fatal("corrupt registry was overwritten")
	}
}

func TestRegisteredSessionCannotLoseItsOwnerAtExpiry(t *testing.T) {
	server := signupServerForTest(t)
	cookie, session := registerAccountForTest(t, server.Handler(), "alice")
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.AddCookie(cookie)
	kind := server.managementAuthorization(req)
	if kind != managementAuthUserSession {
		t.Fatal("expected registered user authentication")
	}
	account, ok := server.accountByID(session.UserID)
	if !ok {
		t.Fatal("account not found")
	}
	parts := strings.Split(cookie.Value, ".")
	parts[2] = strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	parts[4] = server.userSignature(account, strings.Join(parts[:4], "."))
	// Simulate a cookie expiring between authentication and context creation.
	req.Header.Set("Cookie", managementSessionCookie+"="+strings.Join(parts, "."))
	if _, valid := server.withAccountContext(req, kind); valid {
		t.Fatal("expired registered session became an administrator context")
	}
}
