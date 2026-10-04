package api

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"subconv-next/internal/authn"
)

type accountSummary struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	Disabled  bool      `json:"disabled"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

var errAccountChanged = errors.New("account changed; reload and retry")
var errAccountNotFound = errors.New("account not found")

func summarizeAccount(account registeredAccount) accountSummary {
	return accountSummary{ID: account.ID, Username: account.Username, CreatedAt: account.CreatedAt, Disabled: account.Disabled}
}

func (s *Server) updateRegisteredAccount(id string, expected *registeredAccount, update func(*registeredAccount)) (registeredAccount, error) {
	s.accountsMu.Lock()
	defer s.accountsMu.Unlock()
	if err := s.loadAccountsLocked(); err != nil {
		return registeredAccount{}, err
	}
	for key, account := range s.accounts {
		if account.ID != id {
			continue
		}
		if expected != nil && (account.Disabled || account.PasswordHash != expected.PasswordHash || account.SessionVersion != expected.SessionVersion) {
			return registeredAccount{}, errAccountChanged
		}
		update(&account)
		next := make(map[string]registeredAccount, len(s.accounts))
		for existingKey, existing := range s.accounts {
			next[existingKey] = existing
		}
		next[key] = account
		if err := s.saveAccountsLocked(next); err != nil {
			return registeredAccount{}, err
		}
		return account, nil
	}
	return registeredAccount{}, errAccountNotFound
}

func validateNewPassword(w http.ResponseWriter, password, confirmation string) bool {
	if len(password) < 8 || len(password) > 72 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_PASSWORD", "password must contain 8–72 bytes")
		return false
	}
	if password != confirmation {
		writeAPIError(w, http.StatusBadRequest, "PASSWORD_MISMATCH", "passwords do not match")
		return false
	}
	return true
}

func writeAccountUpdateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAccountNotFound):
		writeAPIError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "account not found")
	case errors.Is(err, errAccountChanged):
		writeAPIError(w, http.StatusConflict, "ACCOUNT_CHANGED", "account changed; reload and retry")
	default:
		writeAPIError(w, http.StatusInternalServerError, "ACCOUNT_SAVE_FAILED", "could not save account; retry later")
	}
}

func (s *Server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if !s.managementLoginConfigured() {
		writeAPIError(w, http.StatusServiceUnavailable, "LOGIN_NOT_CONFIGURED", "management account is not configured")
		return
	}
	account, registered := s.registeredAccountForRequest(r)
	if !registered {
		writeAPIError(w, http.StatusForbidden, "ADMINISTRATOR_SCRIPT_ONLY", "administrator credentials can only be set using scn account (script menu 6)")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req changePasswordRequest
	if decodeJSONBody(r, &req) != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid password request")
		return
	}
	if !validateNewPassword(w, req.NewPassword, req.ConfirmPassword) {
		return
	}
	release, ok := s.acquirePasswordSlot(w)
	if !ok {
		return
	}
	defer release()
	if !authn.VerifyPassword(account.PasswordHash, req.CurrentPassword) {
		writeAPIError(w, http.StatusBadRequest, "CURRENT_PASSWORD_INVALID", "current password is incorrect")
		return
	}
	hash, err := authn.HashPassword(req.NewPassword)
	if err != nil {
		writeAccountUpdateError(w, err)
		return
	}
	next, err := s.updateRegisteredAccount(account.ID, &account, func(a *registeredAccount) { a.PasswordHash = hash; a.SessionVersion++ })
	if err != nil {
		writeAccountUpdateError(w, err)
		return
	}
	s.finishAccountLogin(w, r, next, http.StatusOK)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	s.accountsMu.Lock()
	if err := s.loadAccountsLocked(); err != nil {
		s.accountsMu.Unlock()
		writeAccountUpdateError(w, err)
		return
	}
	users := make([]accountSummary, 0, len(s.accounts))
	for _, account := range s.accounts {
		users = append(users, summarizeAccount(account))
	}
	s.accountsMu.Unlock()
	sort.Slice(users, func(i, j int) bool { return strings.ToLower(users[i].Username) < strings.ToLower(users[j].Username) })
	writeJSON(w, http.StatusOK, struct {
		OK    bool             `json:"ok"`
		Users []accountSummary `json:"users"`
		Limit int              `json:"limit"`
	}{true, users, maxRegisteredAccounts})
}

func (s *Server) handleUserSubroutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/users/"), "/")
	if len(parts) == 0 || !validAccountID(parts[0]) || len(parts) > 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if len(parts) == 1 {
		if r.Method != http.MethodPatch {
			methodNotAllowed(w, http.MethodPatch)
			return
		}
		var req struct {
			Disabled *bool `json:"disabled"`
		}
		if decodeJSONBody(r, &req) != nil || req.Disabled == nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "disabled must be true or false")
			return
		}
		account, err := s.updateRegisteredAccount(id, nil, func(a *registeredAccount) {
			if a.Disabled != *req.Disabled {
				a.Disabled = *req.Disabled
				a.SessionVersion++
			}
		})
		if err != nil {
			writeAccountUpdateError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			OK   bool           `json:"ok"`
			User accountSummary `json:"user"`
		}{true, summarizeAccount(account)})
		return
	}
	if parts[1] != "password" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		NewPassword     string `json:"new_password"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if decodeJSONBody(r, &req) != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid password request")
		return
	}
	if !validateNewPassword(w, req.NewPassword, req.ConfirmPassword) {
		return
	}
	release, ok := s.acquirePasswordSlot(w)
	if !ok {
		return
	}
	defer release()
	hash, err := authn.HashPassword(req.NewPassword)
	if err != nil {
		writeAccountUpdateError(w, err)
		return
	}
	_, err = s.updateRegisteredAccount(id, nil, func(a *registeredAccount) { a.PasswordHash = hash; a.SessionVersion++ })
	if err != nil {
		writeAccountUpdateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, genericOKResponse{OK: true})
}
