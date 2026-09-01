package api

import (
	"net/http"
	"sort"
	"strings"
	"sync"
)

type workspaceLockEntry struct {
	mu   sync.Mutex
	refs int
}

type workspaceLockManager struct {
	mu      sync.Mutex
	entries map[string]*workspaceLockEntry
}

func (m *workspaceLockManager) lock(key string) func() {
	key = strings.TrimSpace(key)
	if key == "" {
		return func() {}
	}

	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]*workspaceLockEntry)
	}
	entry := m.entries[key]
	if entry == nil {
		entry = &workspaceLockEntry{}
		m.entries[key] = entry
	}
	entry.refs++
	m.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		m.mu.Lock()
		entry.refs--
		if entry.refs == 0 && m.entries[key] == entry {
			delete(m.entries, key)
		}
		m.mu.Unlock()
	}
}

func (m *workspaceLockManager) tryLock(key string) (func(), bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return func() {}, true
	}

	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]*workspaceLockEntry)
	}
	entry := m.entries[key]
	if entry == nil {
		entry = &workspaceLockEntry{}
		m.entries[key] = entry
	}
	entry.refs++
	m.mu.Unlock()

	if !entry.mu.TryLock() {
		m.mu.Lock()
		entry.refs--
		if entry.refs == 0 && m.entries[key] == entry {
			delete(m.entries, key)
		}
		m.mu.Unlock()
		return nil, false
	}
	return func() {
		entry.mu.Unlock()
		m.mu.Lock()
		entry.refs--
		if entry.refs == 0 && m.entries[key] == entry {
			delete(m.entries, key)
		}
		m.mu.Unlock()
	}, true
}

func (s *Server) lockWorkspaceHash(hash string) func() {
	return s.workspaceLocks.lock(hash)
}

func (s *Server) lockWorkspaceHashes(hashes ...string) func() {
	unique := make(map[string]struct{}, len(hashes))
	ordered := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		hash = strings.TrimSpace(hash)
		if hash == "" {
			continue
		}
		if _, exists := unique[hash]; exists {
			continue
		}
		unique[hash] = struct{}{}
		ordered = append(ordered, hash)
	}
	sort.Strings(ordered)
	unlocks := make([]func(), 0, len(ordered))
	for _, hash := range ordered {
		unlocks = append(unlocks, s.lockWorkspaceHash(hash))
	}
	return func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
}

func (s *Server) workspaceLockMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash := workspaceHashFromRequest(r)
		if hash == "" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/workspaces/") && strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/restore-from-published") {
			// The handler locks source and target workspaces together after it
			// resolves the publication source.
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/refresh" {
			unlock, ok := s.workspaceLocks.tryLock(hash)
			if !ok {
				writeAPIError(w, http.StatusConflict, "REFRESH_IN_PROGRESS", "another workspace operation is already running")
				return
			}
			defer unlock()
			next.ServeHTTP(w, r)
			return
		}
		unlock := s.lockWorkspaceHash(hash)
		defer unlock()
		next.ServeHTTP(w, r)
	})
}

func workspaceHashFromRequest(r *http.Request) string {
	if r == nil || !strings.HasPrefix(r.URL.Path, "/api/") {
		return ""
	}
	workspaceID := ""
	if strings.HasPrefix(r.URL.Path, "/api/workspaces/") {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/workspaces/"), "/")
		workspaceID = strings.TrimSpace(strings.Split(path, "/")[0])
	}
	if workspaceID == "" {
		workspaceID = workspaceIDFromRequest(r)
	}
	if workspaceID == "" {
		return ""
	}
	return sha256Hex(workspaceID)
}
