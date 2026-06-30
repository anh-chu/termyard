package recovery

import (
	"context"
	"reflect"
	"testing"
)

type mockRebuildClient struct {
	hasSession bool
	calls      []string
}

func (m *mockRebuildClient) HasSession(name string) bool {
	m.calls = append(m.calls, "HasSession:"+name)
	return m.hasSession
}
func (m *mockRebuildClient) NewSession(name, projectPath, command string) error {
	m.calls = append(m.calls, "NewSession:"+name+":"+projectPath+":"+command)
	return nil
}
func (m *mockRebuildClient) NewWindow(session, name, projectPath, command string) error {
	m.calls = append(m.calls, "NewWindow:"+session+":"+name+":"+projectPath+":"+command)
	return nil
}
func (m *mockRebuildClient) SplitWindow(target, projectPath, command string) error {
	m.calls = append(m.calls, "SplitWindow:"+target+":"+projectPath+":"+command)
	return nil
}
func (m *mockRebuildClient) SelectLayout(target, layout string) error {
	m.calls = append(m.calls, "SelectLayout:"+target+":"+layout)
	return nil
}
func (m *mockRebuildClient) SelectWindow(session, index string) error {
	m.calls = append(m.calls, "SelectWindow:"+session+":"+index)
	return nil
}
func (m *mockRebuildClient) SelectPane(target string) error {
	m.calls = append(m.calls, "SelectPane:"+target)
	return nil
}
func (m *mockRebuildClient) SetScheduleID(name, scheduleID string) error {
	m.calls = append(m.calls, "SetScheduleID:"+name+":"+scheduleID)
	return nil
}

func TestBuildStartCommand(t *testing.T) {
	tests := []struct {
		name, agentType, token, want string
	}{
		{"pi resume", "pi", "abc", "pi --resume 'abc'"},
		{"claude fresh", "claude", "", "claude"},
		{"codex resume", "codex", "t-1", "codex resume 't-1'"},
		{"opencode resume", "opencode", "s-1", "opencode --session 's-1'"},
		{"plain", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildStartCommand(tt.agentType, tt.token, "/tmp", "bash")
			if got != tt.want {
				t.Fatalf("buildStartCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRebuildSkipsExistingSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &Manifest{Version: CurrentVersion, Sessions: []SessionSnapshot{{Name: "s1", Windows: []WindowSnapshot{{Index: 0, Panes: []PaneSnapshot{{Index: 0, CWD: "/tmp"}}}}}}}
	if err := m.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	h := &mockRebuildClient{hasSession: true}
	r := NewRebuilder(h, nil, nil)
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild() failed: %v", err)
	}
	if got := h.calls; !reflect.DeepEqual(got, []string{"HasSession:s1"}) {
		t.Fatalf("expected has-session probe, got %#v", got)
	}
}

func TestRebuildOrdersWindowsPanes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := &Manifest{
		Version: CurrentVersion,
		Sessions: []SessionSnapshot{{
			Name:           "s1",
			AgentType:      "claude",
			AgentSessionID: "abc",
			Windows: []WindowSnapshot{{
				Index:  0,
				Name:   "main",
				Layout: "even-horizontal",
				Active: true,
				Panes:  []PaneSnapshot{{Index: 0, CWD: "/tmp/a"}, {Index: 1, CWD: "/tmp/b", Active: true}},
			}, {
				Index:  1,
				Name:   "logs",
				Layout: "tiled",
				Panes:  []PaneSnapshot{{Index: 0, CWD: "/tmp/c"}},
			}},
		}},
	}
	if err := m.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	h := &mockRebuildClient{}
	r := NewRebuilder(h, nil, nil)
	if err := r.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild() failed: %v", err)
	}
	want := []string{
		"HasSession:s1",
		"NewSession:s1:/tmp/a:claude --resume 'abc'",
		"SplitWindow:s1:0:/tmp/b:claude --resume 'abc'",
		"SelectLayout:s1:0:even-horizontal",
		"SelectPane:s1:0.1",
		"NewWindow:s1:1:logs:/tmp/c:claude --resume 'abc'",
		"SelectLayout:s1:1:tiled",
		"SelectWindow:s1:0",
	}
	if !reflect.DeepEqual(h.calls, want) {
		t.Fatalf("calls mismatch\n got=%#v\nwant=%#v", h.calls, want)
	}
}
