package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"subconv-next/internal/authn"
	"testing"
)

func adminForUsersTest(t *testing.T, server *Server) (*http.Cookie, string) {
	t.Helper()
	return loginAccountForTest(t, server.Handler(), "127.0.0.1:1234", "admin", "a-strong-management-token")
}

func TestUserManagementPermissionAndSecretRedaction(t *testing.T) {
	server := signupServerForTest(t)
	handler := server.Handler()
	user, session := registerAccountForTest(t, handler, "alice")
	admin, csrf := adminForUsersTest(t, server)
	for _, route := range []struct {
		method, path string
		payload      any
	}{
		{http.MethodGet, "/api/users", nil},
		{http.MethodPatch, "/api/users/" + session.UserID, map[string]bool{"disabled": true}},
		{http.MethodPost, "/api/users/" + session.UserID + "/password", map[string]string{"new_password": "replacement123", "confirm_password": "replacement123"}},
	} {
		rec := accountRequestForTest(t, handler, route.method, route.path, route.payload, user, session.CSRFToken)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("user route %s: %d %s", route.path, rec.Code, rec.Body.String())
		}
	}
	rec := accountRequestForTest(t, handler, http.MethodGet, "/api/users", nil, nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous user list: %d", rec.Code)
	}
	rec = accountRequestForTest(t, handler, http.MethodGet, "/api/users", nil, admin, csrf)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alice") || strings.Contains(rec.Body.String(), "password") || strings.Contains(rec.Body.String(), "session_version") {
		t.Fatalf("user list: %d %s", rec.Code, rec.Body.String())
	}
	rec = accountRequestForTest(t, handler, http.MethodPatch, "/api/users/"+session.UserID, map[string]bool{"disabled": true}, admin, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d", rec.Code)
	}
	rec = accountRequestForTest(t, handler, http.MethodPatch, "/api/users/"+session.UserID, map[string]string{"role": "admin"}, admin, csrf)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("role injection: %d", rec.Code)
	}
}

func TestDisableEnableAndResetInvalidateOldUserSessions(t *testing.T) {
	server := signupServerForTest(t)
	handler := server.Handler()
	user, session := registerAccountForTest(t, handler, "alice")
	workspace := accountWorkspaceForTest(t, handler, user, session)
	admin, csrf := adminForUsersTest(t, server)
	for _, disabled := range []bool{true, false} {
		rec := accountRequestForTest(t, handler, http.MethodPatch, "/api/users/"+session.UserID, map[string]bool{"disabled": disabled}, admin, csrf)
		if rec.Code != http.StatusOK {
			t.Fatalf("toggle: %d %s", rec.Code, rec.Body.String())
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(user)
		if _, _, _, valid := server.userSession(req); valid {
			t.Fatal("old user session survived toggle")
		}
		rec = accountRequestForTest(t, handler, http.MethodPost, "/api/auth/login", authLoginRequest{"alice", "  user password!  "}, nil, "")
		want := http.StatusOK
		if disabled {
			want = http.StatusUnauthorized
		}
		if rec.Code != want {
			t.Fatalf("disabled=%v login=%d", disabled, rec.Code)
		}
	}
	if _, err := server.loadWorkspace(workspace); err != nil {
		t.Fatalf("toggle removed workspace: %v", err)
	}
	rec := accountRequestForTest(t, handler, http.MethodPost, "/api/auth/login", authLoginRequest{"alice", "  user password!  "}, nil, "")
	oldCookie := rec.Result().Cookies()[0]
	rec = accountRequestForTest(t, handler, http.MethodPost, "/api/users/"+session.UserID+"/password", map[string]string{"new_password": "replacement123", "confirm_password": "replacement123"}, admin, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(oldCookie)
	if _, _, _, valid := server.userSession(req); valid {
		t.Fatal("old session survived reset")
	}
	restarted := NewServer(server.version, server.snapshotConfig())
	for _, password := range []string{"  user password!  ", "replacement123"} {
		rec = accountRequestForTest(t, restarted.Handler(), http.MethodPost, "/api/auth/login", authLoginRequest{"alice", password}, nil, "")
		want := http.StatusOK
		if password != "replacement123" {
			want = http.StatusUnauthorized
		}
		if rec.Code != want {
			t.Fatalf("password after restart: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestUserChangesOwnPasswordWithCurrentPasswordAndFreshSession(t *testing.T) {
	server := signupServerForTest(t)
	handler := server.Handler()
	user, session := registerAccountForTest(t, handler, "alice")
	bob, _ := registerAccountForTest(t, handler, "bob")
	admin, _ := adminForUsersTest(t, server)
	req := changePasswordRequest{"incorrect", "new user password!", "new user password!"}
	rec := accountRequestForTest(t, handler, http.MethodPost, "/api/auth/password", req, user, session.CSRFToken)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "CURRENT_PASSWORD_INVALID") {
		t.Fatalf("wrong old password: %d %s", rec.Code, rec.Body.String())
	}
	req.CurrentPassword = "  user password!  "
	rec = accountRequestForTest(t, handler, http.MethodPost, "/api/auth/password", req, user, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("password without CSRF: %d", rec.Code)
	}
	rec = accountRequestForTest(t, handler, http.MethodPost, "/api/auth/password", req, user, session.CSRFToken)
	var next authSessionResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &next) != nil || next.Role != "user" || next.CSRFToken == session.CSRFToken {
		t.Fatalf("change password: %d %s", rec.Code, rec.Body.String())
	}
	for _, check := range []struct {
		cookie *http.Cookie
		valid  bool
	}{{user, false}, {rec.Result().Cookies()[0], true}, {bob, true}, {admin, true}} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(check.cookie)
		valid := server.managementAuthorization(r) != managementAuthNone
		if valid != check.valid {
			t.Fatalf("session valid=%v want=%v", valid, check.valid)
		}
	}
	data, _ := os.ReadFile(filepath.Join(server.baseDataDir(), "accounts.json"))
	if strings.Contains(string(data), req.NewPassword) {
		t.Fatal("plaintext password persisted")
	}
	old, _ := server.accountByUsername("alice")
	_, err := server.updateRegisteredAccount(old.ID, &registeredAccount{ID: old.ID, PasswordHash: "stale"}, func(a *registeredAccount) { a.PasswordHash = "bad" })
	if err != errAccountChanged {
		t.Fatalf("stale change: %v", err)
	}
}

func TestAdministratorCredentialsCanOnlyBeChangedByScript(t *testing.T) {
	server := signupServerForTest(t)
	handler := server.Handler()
	_, userSession := registerAccountForTest(t, handler, "alice")
	admin, csrf := adminForUsersTest(t, server)
	filename := filepath.Join(server.baseDataDir(), "accounts.json")
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	payload := changePasswordRequest{"a-strong-management-token", "independent-root123", "independent-root123"}
	rec := accountRequestForTest(t, handler, http.MethodPost, "/api/auth/password", payload, admin, csrf)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "ADMINISTRATOR_SCRIPT_ONLY") || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("admin web change: %d %s", rec.Code, rec.Body.String())
	}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/password", strings.NewReader(`{"current_password":"a-strong-management-token","new_password":"independent-root123","confirm_password":"independent-root123"}`))
	request.Header.Set("Authorization", "Bearer a-strong-management-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "ADMINISTRATOR_SCRIPT_ONLY") {
		t.Fatalf("admin API-token change: %d %s", response.Code, response.Body.String())
	}
	for _, route := range []string{"/api/users/admin", "/api/users/admin/password"} {
		method := http.MethodPatch
		body := any(map[string]bool{"disabled": true})
		if strings.HasSuffix(route, "/password") {
			method = http.MethodPost
			body = payload
		}
		rec = accountRequestForTest(t, handler, method, route, body, admin, csrf)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("admin as registered user: %d", rec.Code)
		}
	}
	rec = accountRequestForTest(t, handler, http.MethodGet, "/api/users", nil, admin, csrf)
	var listing struct {
		Users []accountSummary `json:"users"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &listing) != nil || len(listing.Users) != 1 || listing.Users[0].ID != userSession.UserID {
		t.Fatalf("registered user list: %d %s", rec.Code, rec.Body.String())
	}
	after, _ := os.ReadFile(filename)
	if string(before) != string(after) || !server.validManagementCredentials("admin", "a-strong-management-token") || server.validManagementCredentials("admin", "independent-root123") {
		t.Fatal("rejected web request changed credentials or account store")
	}
	cfg := server.snapshotConfig()
	cfg.Service.ManagementPasswordHash, err = authn.HashPassword("terminal-reset123")
	if err != nil {
		t.Fatal(err)
	}
	reset := NewServer(server.version, cfg)
	if !reset.validManagementCredentials("admin", "terminal-reset123") || reset.validManagementCredentials("admin", "a-strong-management-token") {
		t.Fatal("script password reset did not take effect")
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(admin)
	if reset.managementAuthorization(request) != managementAuthNone {
		t.Fatal("script password reset preserved old admin cookie")
	}
}

func TestAdministratorLoginSurvivesCorruptRegisteredAccountStore(t *testing.T) {
	server := signupServerForTest(t)
	filename := filepath.Join(server.baseDataDir(), "accounts.json")
	if err := os.WriteFile(filename, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if !server.validManagementCredentials("admin", "a-strong-management-token") {
		t.Fatal("registered account corruption blocked script-configured administrator")
	}
	admin, csrf := adminForUsersTest(t, server)
	rec := accountRequestForTest(t, server.Handler(), http.MethodGet, "/api/users", nil, admin, csrf)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "ACCOUNT_SAVE_FAILED") {
		t.Fatalf("corrupt list: %d %s", rec.Code, rec.Body.String())
	}
	data, _ := os.ReadFile(filename)
	if string(data) != "broken" {
		t.Fatal("corrupt registered account store was overwritten")
	}
	cookie := &http.Cookie{Name: managementSessionCookie, Value: "9999999999.nonce."}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	if _, _, ok := server.managementSession(request); ok {
		t.Fatal("accepted empty signature")
	}
}
