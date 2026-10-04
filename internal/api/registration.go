package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"subconv-next/internal/authn"
	"subconv-next/internal/storage"
)

const maxRegisteredAccounts = 256

type registeredAccount struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	PasswordHash   string    `json:"password_hash"`
	CreatedAt      time.Time `json:"created_at"`
	Disabled       bool      `json:"disabled,omitempty"`
	SessionVersion uint64    `json:"session_version,omitempty"`
}

type accountStore struct {
	Version  int                          `json:"version"`
	Accounts map[string]registeredAccount `json:"accounts"`
}

type authRegisterRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirm_password"`
}

type accountContextKey struct{}

var (
	errAccountExists = errors.New("account already exists")
	errAccountLimit  = errors.New("registration capacity reached")
)

func (s *Server) registrationEnabled() bool {
	service := s.snapshotConfig().Service
	return service.RegistrationEnabled && !service.PublicConverter && !service.AllowInsecurePublic && s.managementLoginConfigured()
}

// Caller holds accountsMu. A corrupt store must never be overwritten by an
// empty registry; restart with a repaired store instead.
func (s *Server) loadAccountsLocked() error {
	if s.accountsLoaded {
		return nil
	}
	file, err := os.Open(filepath.Join(s.baseDataDir(), "accounts.json"))
	if errors.Is(err, os.ErrNotExist) {
		s.accounts = make(map[string]registeredAccount)
		s.accountsLoaded = true
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return errors.New("could not read account store")
	}
	var store accountStore
	if json.Unmarshal(data, &store) != nil || store.Version != 1 || len(store.Accounts) > maxRegisteredAccounts {
		return errors.New("invalid account store")
	}
	ids := make(map[string]bool)
	for key, account := range store.Accounts {
		if key != strings.ToLower(account.Username) || !authn.ValidUsername(account.Username) || !validAccountID(account.ID) || ids[account.ID] || authn.ValidateHash(account.PasswordHash) != nil {
			return errors.New("invalid account store record")
		}
		ids[account.ID] = true
	}
	if store.Accounts == nil {
		store.Accounts = make(map[string]registeredAccount)
	}
	s.accounts = store.Accounts
	s.accountsLoaded = true
	return nil
}

func validAccountID(id string) bool {
	if !strings.HasPrefix(id, "u_") {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, "u_"))
	return err == nil && len(decoded) == 18
}

func (s *Server) accountByUsername(username string) (registeredAccount, bool) {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	if s.loadAccountsLocked() != nil {
		return registeredAccount{}, false
	}
	account, ok := s.accounts[strings.ToLower(strings.TrimSpace(username))]
	return account, ok
}

func (s *Server) accountByID(id string) (registeredAccount, bool) {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	if s.loadAccountsLocked() != nil {
		return registeredAccount{}, false
	}
	for _, account := range s.accounts {
		if account.ID == id {
			return account, true
		}
	}
	return registeredAccount{}, false
}

func (s *Server) createAccount(username, password string) (registeredAccount, error) {
	hash, err := authn.HashPassword(password)
	if err != nil {
		return registeredAccount{}, err
	}
	identifier := make([]byte, 18)
	if _, err := rand.Read(identifier); err != nil {
		return registeredAccount{}, err
	}
	account := registeredAccount{ID: "u_" + base64.RawURLEncoding.EncodeToString(identifier), Username: username, PasswordHash: hash, CreatedAt: time.Now().UTC()}
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	if err := s.loadAccountsLocked(); err != nil {
		return registeredAccount{}, err
	}
	key := strings.ToLower(username)
	if _, exists := s.accounts[key]; exists {
		return registeredAccount{}, errAccountExists
	}
	if len(s.accounts) >= maxRegisteredAccounts {
		return registeredAccount{}, errAccountLimit
	}
	next := make(map[string]registeredAccount, len(s.accounts)+1)
	for key, existing := range s.accounts {
		next[key] = existing
	}
	next[key] = account
	if err := s.saveAccountsLocked(next); err != nil {
		return registeredAccount{}, err
	}
	return account, nil
}

// Caller holds accountsMu. Publish memory changes only after durable storage succeeds.
func (s *Server) saveAccountsLocked(next map[string]registeredAccount) error {
	data, err := json.MarshalIndent(accountStore{Version: 1, Accounts: next}, "", "  ")
	if err != nil {
		return err
	}
	if err := storage.AtomicWriteFile(filepath.Join(s.baseDataDir(), "accounts.json"), append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.accounts = next
	return nil
}

func (s *Server) acquirePasswordSlot(w http.ResponseWriter) (func(), bool) {
	select {
	case s.passwordSlots <- struct{}{}:
		return func() { <-s.passwordSlots }, true
	default:
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusServiceUnavailable, "AUTH_BUSY", "authentication service is busy; retry shortly")
		return nil, false
	}
}

func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.registrationEnabled() {
		writeAPIError(w, http.StatusForbidden, "REGISTRATION_DISABLED", "registration is disabled")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req authRegisterRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid registration request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || !authn.ValidUsername(req.Username) {
		writeAPIError(w, http.StatusBadRequest, "INVALID_USERNAME", "username must contain 3–64 letters, digits, _, ., @ or -")
		return
	}
	if strings.EqualFold(req.Username, s.managementUsername()) || strings.EqualFold(req.Username, authn.DefaultUsername) {
		writeAPIError(w, http.StatusConflict, "ACCOUNT_EXISTS", "this username is unavailable")
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 72 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_PASSWORD", "password must contain 8–72 bytes")
		return
	}
	if req.Password != req.ConfirmPassword {
		writeAPIError(w, http.StatusBadRequest, "PASSWORD_MISMATCH", "passwords do not match")
		return
	}
	if _, exists := s.accountByUsername(req.Username); exists {
		writeAPIError(w, http.StatusConflict, "ACCOUNT_EXISTS", "this username is unavailable")
		return
	}
	release, ok := s.acquirePasswordSlot(w)
	if !ok {
		return
	}
	defer release()
	account, err := s.createAccount(req.Username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, errAccountExists):
			writeAPIError(w, http.StatusConflict, "ACCOUNT_EXISTS", "this username is unavailable")
		case errors.Is(err, errAccountLimit):
			writeAPIError(w, http.StatusTooManyRequests, "REGISTRATION_FULL", "registration capacity reached")
		default:
			writeAPIError(w, http.StatusInternalServerError, "REGISTRATION_FAILED", "could not save account; retry later")
		}
		return
	}
	s.finishAccountLogin(w, r, account, http.StatusCreated)
}

func (s *Server) userSignature(account registeredAccount, payload string) string {
	if account.SessionVersion != 0 {
		payload = "revision\x00" + strconv.FormatUint(account.SessionVersion, 10) + "\x00" + payload
	}
	return s.managementSignature("registered-session\x00" + account.ID + "\x00" + account.PasswordHash + "\x00" + payload)
}

func (s *Server) newUserSession(account registeredAccount) (string, time.Time, error) {
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		return "", time.Time{}, fmt.Errorf("generate session nonce: %w", err)
	}
	expiresAt := time.Now().UTC().Add(managementSessionTTL)
	payload := "u." + account.ID + "." + strconv.FormatInt(expiresAt.Unix(), 10) + "." + base64.RawURLEncoding.EncodeToString(nonce)
	signature := s.userSignature(account, payload)
	if signature == "" {
		return "", time.Time{}, errors.New("could not sign login session")
	}
	return payload + "." + signature, expiresAt, nil
}

func (s *Server) userSession(r *http.Request) (registeredAccount, string, time.Time, bool) {
	cookie, err := r.Cookie(managementSessionCookie)
	if err != nil || len(cookie.Value) > 512 {
		return registeredAccount{}, "", time.Time{}, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 5 || parts[0] != "u" || !validAccountID(parts[1]) {
		return registeredAccount{}, "", time.Time{}, false
	}
	expiresUnix, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return registeredAccount{}, "", time.Time{}, false
	}
	expiresAt := time.Unix(expiresUnix, 0).UTC()
	now := time.Now().UTC()
	if !expiresAt.After(now) || expiresAt.After(now.Add(managementSessionTTL+time.Minute)) {
		return registeredAccount{}, "", time.Time{}, false
	}
	account, found := s.accountByID(parts[1])
	if !found || account.Disabled {
		return registeredAccount{}, "", time.Time{}, false
	}
	expected := s.userSignature(account, strings.Join(parts[:4], "."))
	if expected == "" || !constantTimeEqual(parts[4], expected) {
		return registeredAccount{}, "", time.Time{}, false
	}
	return account, cookie.Value, expiresAt, true
}

func (s *Server) finishAccountLogin(w http.ResponseWriter, r *http.Request, account registeredAccount, status int) {
	sessionValue, expiresAt, err := s.newUserSession(account)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "SESSION_FAILED", "could not create login session")
		return
	}
	s.setSessionCookie(w, r, sessionValue, expiresAt)
	writeJSON(w, status, authSessionResponse{Version: s.version, OK: true, Required: true, Configured: true, Authenticated: true, RegistrationEnabled: s.registrationEnabled(), Username: account.Username, UserID: account.ID, Role: "user", ExpiresAt: expiresAt.Format(time.RFC3339), CSRFToken: s.managementCSRFToken(sessionValue)})
}

func (s *Server) registeredAccountForRequest(r *http.Request) (registeredAccount, bool) {
	if account, ok := r.Context().Value(accountContextKey{}).(registeredAccount); ok {
		return account, account.ID != ""
	}
	account, _, _, ok := s.userSession(r)
	return account, ok
}

func (s *Server) restrictedRequest(r *http.Request) bool {
	_, registered := s.registeredAccountForRequest(r)
	return s.snapshotConfig().Service.PublicConverter || registered
}

func (s *Server) withAccountContext(r *http.Request, kind managementAuthKind) (*http.Request, bool) {
	var account registeredAccount
	if kind == managementAuthUserSession {
		var valid bool
		account, _, _, valid = s.userSession(r)
		if !valid {
			return r, false
		}
	}
	return r.WithContext(context.WithValue(r.Context(), accountContextKey{}, account)), true
}

func (s *Server) workspaceAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		id := workspaceIDFromRequest(r)
		if strings.HasPrefix(r.URL.Path, "/api/workspaces/") {
			id = strings.Split(strings.TrimPrefix(r.URL.Path, "/api/workspaces/"), "/")[0]
		}
		if id != "" {
			// Check ownership before handlers touch or expire a workspace.
			ref, err := s.loadWorkspaceByHash(sha256Hex(id))
			if err != nil {
				handleWorkspaceError(w, err)
				return
			}
			if !s.workspaceAccessible(r, ref) {
				handleWorkspaceError(w, errWorkspaceNotFound)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) workspaceAccessible(r *http.Request, ref workspaceRef) bool {
	if account, registered := s.registeredAccountForRequest(r); registered {
		return ref.Meta.OwnerID == account.ID
	}
	return !s.snapshotConfig().Service.PublicConverter || ref.Meta.OwnerID == ""
}

func (s *Server) publishedAccessible(r *http.Request, ref publishedRef) bool {
	if account, registered := s.registeredAccountForRequest(r); registered {
		return ref.Meta.OwnerID == account.ID
	}
	return !s.snapshotConfig().Service.PublicConverter || ref.Meta.OwnerID == ""
}
