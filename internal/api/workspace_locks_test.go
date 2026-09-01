package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"subconv-next/internal/model"
)

func TestWorkspaceLockManagerSerializesAndReleasesEntries(t *testing.T) {
	var locks workspaceLockManager
	firstUnlock := locks.lock("workspace-hash")
	acquired := make(chan struct{})
	released := make(chan struct{})
	go func() {
		unlock := locks.lock("workspace-hash")
		close(acquired)
		unlock()
		close(released)
	}()

	select {
	case <-acquired:
		t.Fatal("second workspace operation acquired the lock before the first released it")
	case <-time.After(20 * time.Millisecond):
	}
	firstUnlock()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("second workspace operation did not acquire the released lock")
	}

	locks.mu.Lock()
	entryCount := len(locks.entries)
	locks.mu.Unlock()
	if entryCount != 0 {
		t.Fatalf("workspace lock entry count = %d, want 0", entryCount)
	}
}

func TestWorkspaceLockManagerTryLockDoesNotWait(t *testing.T) {
	var locks workspaceLockManager
	unlock := locks.lock("workspace-hash")
	if secondUnlock, ok := locks.tryLock("workspace-hash"); ok {
		secondUnlock()
		unlock()
		t.Fatal("tryLock acquired an already-held workspace lock")
	}
	unlock()
	secondUnlock, ok := locks.tryLock("workspace-hash")
	if !ok {
		t.Fatal("tryLock rejected a released workspace lock")
	}
	secondUnlock()
}

func TestLockWorkspaceHashesUsesStableOrder(t *testing.T) {
	server, _ := newTestServer(t, model.DefaultConfig())
	firstAcquired := make(chan struct{})
	firstRelease := make(chan struct{})
	done := make(chan struct{})
	go func() {
		unlock := server.lockWorkspaceHashes("b", "a")
		close(firstAcquired)
		<-firstRelease
		unlock()
	}()
	<-firstAcquired
	go func() {
		unlock := server.lockWorkspaceHashes("a", "b")
		unlock()
		close(done)
	}()
	close(firstRelease)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workspace multi-lock acquisition deadlocked")
	}
}

func TestWorkspaceHashFromRequestUsesPathOrCapabilityQuery(t *testing.T) {
	workspaceID := "w_test-capability"
	for _, target := range []string{
		"/api/config?workspace=" + workspaceID,
		"/api/workspaces/" + workspaceID + "/restore-draft",
	} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		if got, want := workspaceHashFromRequest(req), sha256Hex(workspaceID); got != want {
			t.Fatalf("workspaceHashFromRequest(%q) = %q, want %q", target, got, want)
		}
	}
}

func TestTouchWorkspaceDoesNotRestoreStalePublishMetadata(t *testing.T) {
	server, _ := newTestServer(t, model.DefaultConfig())
	ref, err := server.createWorkspace()
	if err != nil {
		t.Fatalf("createWorkspace() error = %v", err)
	}
	stale := ref
	ref.Meta.PublishID = "p_current-binding"
	if err := server.saveWorkspaceMeta(ref); err != nil {
		t.Fatalf("saveWorkspaceMeta() error = %v", err)
	}
	if err := server.touchWorkspace(&stale); err != nil {
		t.Fatalf("touchWorkspace() error = %v", err)
	}
	current, err := server.loadWorkspaceByHash(ref.Hash)
	if err != nil {
		t.Fatalf("loadWorkspaceByHash() error = %v", err)
	}
	if current.Meta.PublishID != ref.Meta.PublishID {
		t.Fatalf("publish ID = %q, want %q after stale touch", current.Meta.PublishID, ref.Meta.PublishID)
	}
	if current.Meta.LastAccessAt.Before(ref.Meta.LastAccessAt) {
		t.Fatalf("last access moved backwards: got %s, previous %s", current.Meta.LastAccessAt, ref.Meta.LastAccessAt)
	}
}
