package state

import (
	"testing"

	"github.com/ekristen/guppi/pkg/tmux"
)

func TestApplyRenameMigratesStateAndBroadcasts(t *testing.T) {
	m := &Manager{
		sessions: map[string]*tmux.Session{
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
	if !meta.TmuxRenamed {
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
