package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"subconv-next/internal/authn"
)

const (
	managementSessionCookie = "subconv_management_session"
	managementSessionTTL    = 12 * time.Hour
	managementCSRFHeader    = "X-SubConv-CSRF"
)

type managementAuthKind uint8

const (
	managementAuthNone managementAuthKind = iota
	managementAuthTrusted
	managementAuthToken
	managementAuthSession
)

type authLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authSessionResponse struct {
	OK              bool   `json:"ok"`
	Required        bool   `json:"required"`
	Configured      bool   `json:"configured"`
	Authenticated   bool   `json:"authenticated"`
	PublicConverter bool   `json:"public_converter,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	CSRFToken       string `json:"csrf_token,omitempty"`
}

func (s *Server) expectedAccessToken() string {
	cfg := s.snapshotConfig()
	token := strings.TrimSpace(cfg.Service.AccessToken)
	if token == "" {
		token = strings.TrimSpace(cfg.Service.SubscriptionToken)
	}
	return token
}

func (s *Server) managementLoginRequired(r *http.Request) bool {
	if s.snapshotConfig().Service.AllowInsecurePublic {
		return false
	}
	if s.managementLoginConfigured() {
		return true
	}
	if !s.publiclyBound() {
		return false
	}
	return true
}

func (s *Server) managementAuthorization(r *http.Request) managementAuthKind {
	if !s.managementLoginRequired(r) {
		return managementAuthTrusted
	}

	expected := s.expectedAccessToken()
	if provided := requestAccessToken(r); expected != "" && provided != "" && constantTimeEqual(provided, expected) {
		return managementAuthToken
	}
	if _, _, ok := s.managementSession(r); ok {
		return managementAuthSession
	}
	return managementAuthNone
}

func (s *Server) authorizeManagementRequest(r *http.Request) bool {
	return s.managementAuthorization(r) != managementAuthNone
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	publicConverter := s.snapshotConfig().Service.PublicConverter
	required := !publicConverter && s.managementLoginRequired(r)
	authKind := s.managementAuthorization(r)
	response := authSessionResponse{
		OK:              true,
		Required:        required,
		Configured:      !publicConverter && s.managementLoginConfigured(),
		Authenticated:   publicConverter || !required || authKind != managementAuthNone,
		PublicConverter: publicConverter,
	}
	if authKind == managementAuthSession {
		if sessionValue, expiresAt, ok := s.managementSession(r); ok {
			response.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
			response.CSRFToken = s.managementCSRFToken(sessionValue)
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.snapshotConfig().Service.PublicConverter {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}

	if !s.managementLoginConfigured() {
		writeAPIError(w, http.StatusServiceUnavailable, "LOGIN_NOT_CONFIGURED", "management account is not configured")
		return
	}

	var req authLoginRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if !s.validManagementCredentials(req.Username, req.Password) {
		s.appendLog("management login rejected from " + s.clientHost(r))
		writeAPIError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "username or password is incorrect")
		return
	}

	sessionValue, expiresAt, err := s.newManagementSession()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_FAILED", "could not create management session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     managementSessionCookie,
		Value:    sessionValue,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(managementSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.requestUsesHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	s.appendLog("management login accepted from " + s.clientHost(r))
	writeJSON(w, http.StatusOK, authSessionResponse{
		OK:            true,
		Required:      true,
		Configured:    true,
		Authenticated: true,
		ExpiresAt:     expiresAt.UTC().Format(time.RFC3339),
		CSRFToken:     s.managementCSRFToken(sessionValue),
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.snapshotConfig().Service.PublicConverter {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     managementSessionCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.requestUsesHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) newManagementSession() (string, time.Time, error) {
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return "", time.Time{}, fmt.Errorf("generate session nonce: %w", err)
	}
	expiresAt := time.Now().UTC().Add(managementSessionTTL)
	payload := strconv.FormatInt(expiresAt.Unix(), 10) + "." + base64.RawURLEncoding.EncodeToString(nonce)
	signature := s.managementSignature("session\x00" + payload)
	return payload + "." + signature, expiresAt, nil
}

func (s *Server) managementSession(r *http.Request) (string, time.Time, bool) {
	cookie, err := r.Cookie(managementSessionCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", time.Time{}, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return "", time.Time{}, false
	}
	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	payload := parts[0] + "." + parts[1]
	expectedSignature := s.managementSignature("session\x00" + payload)
	if !constantTimeEqual(parts[2], expectedSignature) {
		return "", time.Time{}, false
	}
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	now := time.Now().UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(managementSessionTTL+time.Minute)) {
		return "", time.Time{}, false
	}
	return cookie.Value, expiresAt, true
}

func (s *Server) managementSignature(value string) string {
	service := s.snapshotConfig().Service
	// Changing the account invalidates old sessions without rotating API tokens
	// or published subscription links. Keep credentials out of the cookie.
	key := sha256.Sum256([]byte(s.expectedAccessToken() + "\x00" + s.managementUsername() + "\x00" + service.ManagementPasswordHash))
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) managementUsername() string {
	if username := strings.TrimSpace(s.snapshotConfig().Service.ManagementUsername); username != "" {
		return username
	}
	return authn.DefaultUsername
}

func (s *Server) managementLoginConfigured() bool {
	return s.snapshotConfig().Service.ManagementPasswordHash != "" || s.expectedAccessToken() != ""
}

func (s *Server) validManagementCredentials(username, password string) bool {
	service := s.snapshotConfig().Service
	usernameOK := constantTimeEqual(strings.TrimSpace(username), s.managementUsername())
	var passwordOK bool
	if service.ManagementPasswordHash != "" {
		passwordOK = authn.VerifyPassword(service.ManagementPasswordHash, password)
	} else {
		// Existing installations use their original token until scn account
		// sets an independent web password. Username is always required.
		expected := s.expectedAccessToken()
		passwordOK = expected != "" && constantTimeEqual(strings.TrimSpace(password), expected)
	}
	return usernameOK && passwordOK
}

func (s *Server) managementCSRFToken(sessionValue string) string {
	return s.managementSignature("csrf\x00" + sessionValue)
}

func (s *Server) validManagementCSRF(r *http.Request) bool {
	sessionValue, _, ok := s.managementSession(r)
	if !ok {
		return false
	}
	provided := strings.TrimSpace(r.Header.Get(managementCSRFHeader))
	return provided != "" && constantTimeEqual(provided, s.managementCSRFToken(sessionValue))
}

func (s *Server) requestUsesHTTPS(r *http.Request) bool {
	service := s.snapshotConfig().Service
	if requestTransportUsesHTTPS(r, service.TrustProxyHeaders) {
		return true
	}
	publicBaseURL, err := url.Parse(strings.TrimSpace(service.PublicBaseURL))
	return err == nil && strings.EqualFold(publicBaseURL.Scheme, "https") && publicBaseURL.Host != ""
}
