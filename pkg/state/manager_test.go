package state

import (
	"testing"

	"github.com/anh-chu/termyard/pkg/model"
)

func TestApplyRenameMigratesStateAndBroadcasts(t *testing.T) {
	m := &Manager{
		sessions: map[string]*model.Session{
			"old": {Name: "old"},
		},
		meta: map[string]SessionMetadata{
			"old": {DisplayName: "label"},
		},
	}

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	m.ApplyRename("old", "new")

	if _, ok := m.sessions["old"]; ok {
		t.Fatalf("old session key still present")
	}
	sess, ok := m.sessions["new"]
	if !ok {
		t.Fatalf("new session key missing")
	}
	if sess.Name != "new" {
		t.Fatalf("session name = %q, want %q", sess.Name, "new")
	}
	meta, ok := m.meta["new"]
	if !ok {
		t.Fatalf("new meta key missing")
	}
	if !meta.Renamed {
		t.Fatalf("TmuxRenamed not set")
	}

	evt := <-ch
	if evt.Type != "session-renamed" {
		t.Fatalf("event type = %q, want session-renamed", evt.Type)
	}
	if evt.Session != "old" {
		t.Fatalf("event session = %q, want old", evt.Session)
	}
	data, ok := evt.Data.(map[string]string)
	if !ok {
		t.Fatalf("event data type = %T, want map[string]string", evt.Data)
	}
	if got := data["new_name"]; got != "new" {
		t.Fatalf("event new_name = %q, want new", got)
	}
}

func TestApplyRenameDoesNotBroadcastWhenMissing(t *testing.T) {
	m := &Manager{}

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	m.ApplyRename("old", "new")

	select {
	case evt := <-ch:
		t.Fatalf("unexpected event: %+v", evt)
	default:
	}
}

// fakeDaemonReg is a minimal DaemonRegistry stub for UpdateSessions tests.
type fakeDaemonReg struct {
	dead map[string]bool
}

func (f *fakeDaemonReg) List() []DaemonSessionInfo              { return nil }
func (f *fakeDaemonReg) Capture(name string) (string, error)    { return "", nil }
func (f *fakeDaemonReg) CrashedSessions() []CrashedSessionInfo { return nil }
func (f *fakeDaemonReg) IsSessionDead(name string) bool         { return f.dead[name] }

// TestUpdateSessions_RemovesConfirmedDeadLastSession verifies that killing
// the last session (discovery goes empty) removes it from state instead of
// skipping the cycle, so it does not linger as "disconnected — reconnecting".
func TestUpdateSessions_RemovesConfirmedDeadLastSession(t *testing.T) {
	m := &Manager{
		sessions: map[string]*model.Session{"solo": {Name: "solo"}},
		meta:     map[string]SessionMetadata{"solo": {}},
		daemonReg: &fakeDaemonReg{dead: map[string]bool{"solo": true}},
	}

	ch := m.Subscribe()
	defer m.Unsubscribe(ch)

	m.UpdateSessions(nil) // discovery returns empty

	if _, ok := m.sessions["solo"]; ok {
		t.Fatalf("confirmed-dead last session was not removed")
	}
}

// TestUpdateSessions_SkipsTransientEmptyDiscovery verifies that an empty
// discovery is still treated as transient (not a real kill) when the tracked
// session is NOT confirmed dead, preserving the mass-removal safety guard.
func TestUpdateSessions_SkipsTransientEmptyDiscovery(t *testing.T) {
	m := &Manager{
		sessions: map[string]*model.Session{"solo": {Name: "solo"}},
		meta:     map[string]SessionMetadata{"solo": {}},
		daemonReg: &fakeDaemonReg{dead: map[string]bool{"solo": false}},
	}

	m.UpdateSessions(nil)

	if _, ok := m.sessions["solo"]; !ok {
		t.Fatalf("live session was wrongly removed on transient empty discovery")
	}
}

