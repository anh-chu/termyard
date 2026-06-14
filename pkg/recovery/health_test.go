package recovery

import (
	"sync/atomic"
	"testing"

	"github.com/ekristen/guppi/pkg/tmux"
)

type mockHealthClient struct {
	alive    bool
	sessions []*tmux.Session
}

func (m *mockHealthClient) ServerAlive() bool                      { return m.alive }
func (m *mockHealthClient) ListSessions() ([]*tmux.Session, error) { return m.sessions, nil }

func TestHealthPollerTriggersOnMissingSessions(t *testing.T) {
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
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("onGone calls = %d, want 1", got)
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
