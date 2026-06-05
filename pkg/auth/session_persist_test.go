package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSessionPersistence verifies that sessions survive a "restart" by
// reloading from disk, and that expired entries are pruned on load.
func TestSessionPersistence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// configDir() resolves to $HOME/.config/guppi
	sm := NewSessionManager(time.Hour)
	token, err := sm.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// File should exist after Create.
	path := filepath.Join(dir, ".config", "guppi", "sessions.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sessions file not written: %v", err)
	}

	// Simulate restart: fresh manager loads from disk.
	sm2 := NewSessionManager(time.Hour)
	if !sm2.Validate(token) {
		t.Fatal("session not restored after restart")
	}

	// Revoke removes and persists.
	sm2.Revoke(token)
	sm3 := NewSessionManager(time.Hour)
	if sm3.Validate(token) {
		t.Fatal("revoked session still valid after restart")
	}
}

// TestExpiredSessionsPrunedOnLoad ensures expired tokens do not survive reload.
func TestExpiredSessionsPrunedOnLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	sm := NewSessionManager(time.Millisecond)
	token, err := sm.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	sm2 := NewSessionManager(time.Millisecond)
	if sm2.Validate(token) {
		t.Fatal("expired session should not be restored")
	}
}
