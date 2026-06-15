package recovery

import (
	"sync/atomic"
	"testing"

	"github.com/anh-chu/termyard/pkg/tmux"
)

type mockHealthClient struct {
	alive    bool
	sessions []*tmux.Session
}

func (m *mockHealthClient) ServerAlive() bool                      { return m.alive }
func (m *mockHealthClient) ListSessions() ([]*tmux.Session, error) { return m.sessions, nil }

// A session missing while the tmux server is still alive is an intentional
// kill, not a crash. Recovery must NOT trigger (the auto-recovery respawn bug).
func TestHealthPollerIgnoresMissingSessionsWhileServerAlive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &Manifest{Version: CurrentVersion, Sessions: []SessionSnapshot{{Name: "s1"}}}
	if err := m.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	var calls int32
	p := NewHealthPoller(&mockHealthClient{alive: true, sessions: nil}, 0, func() {
		atomic.AddInt32(&calls, 1)
	})
	p.probe()
	p.probe()
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("onGone calls = %d, want 0", got)
	}
}

// Server death (crash) with a non-empty manifest must trigger recovery once.
func TestHealthPollerTriggersOnServerDeath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &Manifest{Version: CurrentVersion, Sessions: []SessionSnapshot{{Name: "s1"}}}
	if err := m.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	var calls int32
	client := &mockHealthClient{alive: true}
	p := NewHealthPoller(client, 0, func() {
		atomic.AddInt32(&calls, 1)
	})
	p.probe()            // alive, records wasAlive
	client.alive = false // server crashes
	p.probe()
	p.probe() // already triggered, must not re-fire
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("onGone calls = %d, want 1", got)
	}
}

// ForgetSession removes only the named session and leaves siblings intact.
func TestForgetSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &Manifest{Version: CurrentVersion, Sessions: []SessionSnapshot{{Name: "s1"}, {Name: "s2"}}}
	if err := m.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}
	if err := ForgetSession("s1"); err != nil {
		t.Fatalf("ForgetSession() failed: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].Name != "s2" {
		t.Fatalf("after ForgetSession, sessions = %+v, want [s2]", got.Sessions)
	}
}

func TestHealthPollerSkipsEmptyManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	var calls int32
	p := NewHealthPoller(&mockHealthClient{alive: false}, 0, func() {
		atomic.AddInt32(&calls, 1)
	})
	p.probe()
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("onGone calls = %d, want 0", got)
	}
}
