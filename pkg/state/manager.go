package state

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ekristen/guppi/pkg/git"
	"github.com/ekristen/guppi/pkg/namer"
	"github.com/ekristen/guppi/pkg/tmux"
	"github.com/ekristen/guppi/pkg/toolevents"
)

type SessionMetadata struct {
	ProjectPath      string
	AgentType        string
	PromptPreview    string
	AgentSessionID   string
	UserPrompt       string // first user message; set once
	LastAgentMessage string // last agent response; always updated
	DisplayName      string // AI-generated friendly label
	UserSetName      bool   // user manually set DisplayName; AI must not overwrite
	NameAssigned     bool   // AI naming already ran for this session (one-shot for agent tmux rename)
}

// Manager holds the central state tree
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*tmux.Session
	client   *tmux.Client
	meta     map[string]SessionMetadata
	namer    *namer.Namer

	// Subscribers for state changes
	subMu       sync.RWMutex
	subscribers []chan StateEvent
}

// StateEvent represents a change in the state tree
type StateEvent struct {
	Type     string      `json:"type"`
	Session  string      `json:"session,omitempty"`
	Host     string      `json:"host,omitempty"`
	HostName string      `json:"host_name,omitempty"`
	Data     interface{} `json:"data,omitempty"`
}

// NewManager creates a new state manager
func NewManager(client *tmux.Client) *Manager {
	return &Manager{
		sessions: make(map[string]*tmux.Session),
		client:   client,
		meta:     make(map[string]SessionMetadata),
	}
}

// SetNamer attaches an optional AI session namer. Safe to pass a disabled
// namer or call with nil; naming becomes a no-op.
func (m *Manager) SetNamer(n *namer.Namer) {
	m.mu.Lock()
	m.namer = n
	m.mu.Unlock()
}

// Subscribe returns a channel that receives state events
func (m *Manager) Subscribe() chan StateEvent {
	ch := make(chan StateEvent, 64)
	m.subMu.Lock()
	m.subscribers = append(m.subscribers, ch)
	m.subMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber channel
func (m *Manager) Unsubscribe(ch chan StateEvent) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for i, sub := range m.subscribers {
		if sub == ch {
			m.subscribers = append(m.subscribers[:i], m.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}

// broadcast sends an event to all subscribers
func (m *Manager) broadcast(evt StateEvent) {
	m.subMu.RLock()
	defer m.subMu.RUnlock()
	for _, ch := range m.subscribers {
		select {
		case ch <- evt:
		default:
			// subscriber too slow, drop event
		}
	}
}

// UpdateSessions takes a snapshot of sessions from discovery, diffs against
// previous state, and broadcasts changes
func (m *Manager) UpdateSessions(sessions []*tmux.Session) {
	// Load full details for each session
	for _, session := range sessions {
		if err := m.loadSessionDetails(session); err != nil {
			logrus.WithError(err).WithField("session", session.Name).Warn("failed to load session details")
		}
	}

	m.mu.Lock()
	// Build new map
	newMap := make(map[string]*tmux.Session, len(sessions))
	for _, s := range sessions {
		newMap[s.Name] = s
	}

	// Detect removed sessions
	for name := range m.sessions {
		if _, ok := newMap[name]; !ok {
			m.mu.Unlock()
			m.broadcast(StateEvent{Type: "session-removed", Session: name})
			m.mu.Lock()
		}
	}

	// Detect added sessions
	for name := range newMap {
		if _, ok := m.sessions[name]; !ok {
			m.mu.Unlock()
			m.broadcast(StateEvent{Type: "session-added", Session: name})
			m.mu.Lock()
		}
	}

	m.sessions = newMap
	m.mu.Unlock()

	// Broadcast a general refresh event
	m.broadcast(StateEvent{Type: "sessions-changed"})
}

// loadSessionDetails fills in windows and panes for a session
func (m *Manager) loadSessionDetails(session *tmux.Session) error {
	windows, err := m.client.ListWindows(session.Name)
	if err != nil {
		return err
	}

	for _, win := range windows {
		panes, err := m.client.ListPanes(win.ID)
		if err != nil {
			logrus.WithError(err).WithField("window", win.Name).Warn("failed to list panes")
			continue
		}
		win.Panes = panes
	}

	session.Windows = windows
	session.ProjectPath = tmux.ResolveProjectPath(windows, "")
	session.AgentType = tmux.InferAgentType(windows, "")

	if pane := tmux.PrimaryPane(windows); pane != nil {
		if content, err := m.client.CapturePaneHistory(pane.ID, -200); err == nil {
			session.PromptPreview = tmux.ExtractPromptPreview(content)
		}
	}

	m.applyMetadata(session)

	// Detect linked git worktrees so the UI can offer cleanup on kill.
	if session.ProjectPath != "" {
		if ok, err := git.IsWorktree(session.ProjectPath); err == nil {
			session.IsWorktree = ok
		} else {
			logrus.WithError(err).WithField("path", session.ProjectPath).Debug("git worktree check failed")
		}
	}

	return nil
}

func (m *Manager) applyMetadata(session *tmux.Session) {
	m.mu.RLock()
	meta := m.meta[session.Name]
	m.mu.RUnlock()

	if meta.ProjectPath != "" && session.ProjectPath == "" {
		session.ProjectPath = meta.ProjectPath
	}
	if session.AgentType == "" {
		session.AgentType = tmux.NormalizeAgentType(meta.AgentType)
	}
	if meta.PromptPreview != "" && session.PromptPreview == "" {
		session.PromptPreview = meta.PromptPreview
	}
	if meta.AgentSessionID != "" {
		session.AgentSessionID = meta.AgentSessionID
	}
	if meta.UserPrompt != "" && session.UserPrompt == "" {
		session.UserPrompt = meta.UserPrompt
	}
	if meta.LastAgentMessage != "" {
		session.LastAgentMessage = meta.LastAgentMessage
	}
	if meta.DisplayName != "" {
		session.DisplayName = meta.DisplayName
	}
	session.UserSetName = meta.UserSetName
}

// UpdateSessionMetadataFromEvent stores stable metadata derived from agent
// hooks so it remains available after transient status events are cleared.
func (m *Manager) UpdateSessionMetadataFromEvent(evt *toolevents.Event) {
	if evt == nil || evt.Session == "" {
		return
	}

	m.mu.Lock()
	meta := m.meta[evt.Session]
	changed := false
	if evt.CWD != "" {
		if meta.ProjectPath != evt.CWD {
			changed = true
		}
		meta.ProjectPath = evt.CWD
	}
	if evt.Tool != "" {
		tool := string(evt.Tool)
		if meta.AgentType != tool {
			changed = true
		}
		meta.AgentType = tool
	}
	// Only update PromptPreview from meaningful (non-transient) messages.
	// Transient active-phase labels like "Working" / "Using tool" must not
	// clobber the last meaningful agent message shown in the sidebar.
	if evt.Message != "" && (meta.PromptPreview == "" || evt.Status != toolevents.StatusActive) {
		if meta.PromptPreview != evt.Message {
			changed = true
		}
		meta.PromptPreview = evt.Message
	}
	if evt.AgentSessionID != "" {
		if meta.AgentSessionID != evt.AgentSessionID {
			changed = true
		}
		meta.AgentSessionID = evt.AgentSessionID
	}
	firstPrompt := false
	if evt.UserPrompt != "" && meta.UserPrompt == "" {
		meta.UserPrompt = evt.UserPrompt
		changed = true
		if !meta.NameAssigned && !meta.UserSetName {
			firstPrompt = true
		}
	}
	if evt.AgentMessage != "" && meta.LastAgentMessage != evt.AgentMessage {
		meta.LastAgentMessage = evt.AgentMessage
		changed = true
	}

	if !changed {
		m.mu.Unlock()
		return
	}

	m.meta[evt.Session] = meta
	if session := m.sessions[evt.Session]; session != nil {
		if session.ProjectPath == "" && meta.ProjectPath != "" {
			session.ProjectPath = meta.ProjectPath
		}
		if session.AgentType == "" && meta.AgentType != "" {
			session.AgentType = tmux.NormalizeAgentType(meta.AgentType)
		}
		if session.PromptPreview == "" && meta.PromptPreview != "" {
			session.PromptPreview = meta.PromptPreview
		}
		if meta.AgentSessionID != "" {
			session.AgentSessionID = meta.AgentSessionID
		}
		if session.UserPrompt == "" && meta.UserPrompt != "" {
			session.UserPrompt = meta.UserPrompt
		}
		if meta.LastAgentMessage != "" {
			session.LastAgentMessage = meta.LastAgentMessage
		}
	}
	m.mu.Unlock()

	m.broadcast(StateEvent{Type: "sessions-changed"})

	if firstPrompt {
		go m.triggerAgentNaming(evt.Session)
	}
}

// triggerAgentNaming runs the AI namer for an agent session on its first user
// prompt. On success it stores a DisplayName and, when the gate passes (session
// not attached, name available), also renames the underlying tmux session.
// One-shot: guarded by meta.NameAssigned.
func (m *Manager) triggerAgentNaming(sessionName string) {
	m.mu.RLock()
	n := m.namer
	meta := m.meta[sessionName]
	sess := m.sessions[sessionName]
	attached := sess != nil && sess.Attached
	projectPath := meta.ProjectPath
	if sess != nil && sess.ProjectPath != "" {
		projectPath = sess.ProjectPath
	}
	nc := namer.Context{
		Kind:       namer.KindAgent,
		Workdir:    projectPath,
		Agent:      meta.AgentType,
		UserPrompt: meta.UserPrompt,
		AgentMsg:   meta.LastAgentMessage,
	}
	alreadyNamed := meta.NameAssigned || meta.UserSetName
	m.mu.RUnlock()

	if n == nil || !n.Enabled() || alreadyNamed {
		return
	}
	if projectPath != "" {
		if branch, err := git.CurrentBranch(projectPath); err == nil {
			nc.Branch = branch
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	name, err := n.Generate(ctx, nc)
	if err != nil {
		logrus.WithError(err).WithField("session", sessionName).Warn("agent session naming failed")
		return
	}
	logrus.WithFields(logrus.Fields{"session": sessionName, "name": name}).Info("agent session named")

	m.applyGeneratedName(sessionName, name, !attached)
}

// applyGeneratedName stores displayName for sessionName and marks NameAssigned.
// When allowRename is true and the name is a valid, non-colliding tmux session
// name, it also renames the tmux session and migrates state/meta keys.
func (m *Manager) applyGeneratedName(sessionName, displayName string, allowRename bool) {
	if displayName == "" {
		return
	}
	m.mu.Lock()
	meta, ok := m.meta[sessionName]
	if !ok {
		meta = SessionMetadata{}
	}
	if meta.UserSetName || meta.NameAssigned {
		m.mu.Unlock()
		return
	}
	meta.DisplayName = displayName
	meta.NameAssigned = true
	m.meta[sessionName] = meta
	if sess := m.sessions[sessionName]; sess != nil {
		sess.DisplayName = displayName
	}

	// Decide tmux rename inside the lock to avoid collision races.
	newName := ""
	if allowRename && displayName != sessionName {
		if tmux.ValidateSessionName(displayName) == nil {
			taken := make(map[string]bool, len(m.sessions))
			for n := range m.sessions {
				taken[n] = true
			}
			cand := namer.Dedup(displayName, taken)
			if tmux.ValidateSessionName(cand) == nil && !taken[cand] {
				newName = cand
			}
		}
	}
	m.mu.Unlock()

	if newName == "" {
		m.broadcast(StateEvent{Type: "sessions-changed"})
		return
	}

	if err := m.client.RenameSession(sessionName, newName); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{"old": sessionName, "new": newName}).Warn("tmux rename for AI name failed")
		m.broadcast(StateEvent{Type: "sessions-changed"})
		return
	}

	// Migrate meta + sessions keys to the new name.
	m.mu.Lock()
	if meta, ok := m.meta[sessionName]; ok {
		delete(m.meta, sessionName)
		m.meta[newName] = meta
	}
	if sess, ok := m.sessions[sessionName]; ok {
		delete(m.sessions, sessionName)
		sess.Name = newName
		m.sessions[newName] = sess
	}
	m.mu.Unlock()

	m.broadcast(StateEvent{Type: "session-removed", Session: sessionName})
	m.broadcast(StateEvent{Type: "session-added", Session: newName})
	m.broadcast(StateEvent{Type: "sessions-changed"})
}

// TriggerShellNaming runs the AI namer for a non-agent shell session and stores
// the result as DisplayName. Unlike agent naming this never renames the tmux
// session and is not one-shot — it refreshes on each new detected process.
// No-ops if the session has an agent type, the name is user-set, or the namer
// is disabled.
func (m *Manager) TriggerShellNaming(sessionName string, commands []string) {
	m.mu.RLock()
	n := m.namer
	meta := m.meta[sessionName]
	sess := m.sessions[sessionName]
	projectPath := meta.ProjectPath
	agentType := meta.AgentType
	if sess != nil {
		if sess.ProjectPath != "" {
			projectPath = sess.ProjectPath
		}
		if sess.AgentType != "" {
			agentType = sess.AgentType
		}
	}
	userSet := meta.UserSetName
	m.mu.RUnlock()

	if n == nil || !n.Enabled() || sess == nil || userSet || agentType != "" || len(commands) == 0 {
		return
	}

	nc := namer.Context{Kind: namer.KindShell, Workdir: projectPath, Commands: commands}
	if projectPath != "" {
		if b, err := git.CurrentBranch(projectPath); err == nil {
			nc.Branch = b
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	name, err := n.Generate(ctx, nc)
	if err != nil {
		logrus.WithError(err).WithField("session", sessionName).Warn("shell session naming failed")
		return
	}
	logrus.WithFields(logrus.Fields{"session": sessionName, "name": name}).Info("shell session named")

	m.mu.Lock()
	meta = m.meta[sessionName]
	if meta.UserSetName {
		m.mu.Unlock()
		return
	}
	if meta.DisplayName == name {
		m.mu.Unlock()
		return
	}
	meta.DisplayName = name
	m.meta[sessionName] = meta
	if s := m.sessions[sessionName]; s != nil {
		s.DisplayName = name
	}
	m.mu.Unlock()
	m.broadcast(StateEvent{Type: "sessions-changed"})
}

// SetDisplayName stores a manual display name for a session and flags it so the
// AI namer never overwrites it. Pass userSet=false to clear the manual flag.
func (m *Manager) SetDisplayName(sessionName, displayName string, userSet bool) {
	m.mu.Lock()
	meta := m.meta[sessionName]
	meta.DisplayName = displayName
	meta.UserSetName = userSet
	if userSet {
		meta.NameAssigned = true
	}
	m.meta[sessionName] = meta
	if sess := m.sessions[sessionName]; sess != nil {
		sess.DisplayName = displayName
		sess.UserSetName = userSet
	}
	m.mu.Unlock()
	m.broadcast(StateEvent{Type: "sessions-changed"})
}

// RemoveSession removes a session from the in-memory state, broadcasting
// removal events. Use this when a tmux session no longer exists but the
// state manager still holds a reference to it.
func (m *Manager) RemoveSession(name string) {
	m.mu.Lock()
	delete(m.sessions, name)
	delete(m.meta, name)
	m.mu.Unlock()
	m.broadcast(StateEvent{Type: "session-removed", Session: name})
	m.broadcast(StateEvent{Type: "sessions-changed"})
}

// SetSessionAgentType explicitly stores an agent type for a session,
// overriding inference. Used when a session is created with a known preset.
func (m *Manager) SetSessionAgentType(sessionName, agentType string) {
	m.mu.Lock()
	meta := m.meta[sessionName]
	meta.AgentType = agentType
	m.meta[sessionName] = meta
	if session := m.sessions[sessionName]; session != nil && session.AgentType == "" {
		session.AgentType = agentType
	}
	m.mu.Unlock()
}

// GetSessionProjectPath returns the ProjectPath for a session, or empty string if unknown.
func (m *Manager) GetSessionProjectPath(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s, ok := m.sessions[name]; ok {
		return s.ProjectPath
	}
	if meta, ok := m.meta[name]; ok {
		return meta.ProjectPath
	}
	return ""
}

// SessionForPane returns the session name for a given pane ID (e.g. "%42").
// Returns empty string if not found.
func (m *Manager) SessionForPane(paneID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, sess := range m.sessions {
		for _, win := range sess.Windows {
			for _, pane := range win.Panes {
				if pane.ID == paneID {
					return sess.Name
				}
			}
		}
	}
	return ""
}

// GetSessions returns all tracked sessions with full details
func (m *Manager) GetSessions() []*tmux.Session {
	// Always refresh from tmux for accuracy
	sessions, err := m.client.ListSessions()
	if err != nil {
		logrus.WithError(err).Warn("failed to list sessions")
		m.mu.RLock()
		defer m.mu.RUnlock()
		result := make([]*tmux.Session, 0, len(m.sessions))
		for _, s := range m.sessions {
			result = append(result, s)
		}
		return result
	}

	// Filter out the control mode session
	filtered := make([]*tmux.Session, 0, len(sessions))
	for _, s := range sessions {
		if s.Name != tmux.ControlSessionName() {
			filtered = append(filtered, s)
		}
	}
	sessions = filtered

	for _, session := range sessions {
		if err := m.loadSessionDetails(session); err != nil {
			logrus.WithError(err).WithField("session", session.Name).Warn("failed to load session details")
		}
	}

	m.mu.Lock()
	m.sessions = make(map[string]*tmux.Session, len(sessions))
	for _, s := range sessions {
		m.sessions[s.Name] = s
	}
	m.mu.Unlock()

	return sessions
}

// SnapshotForManifest returns deep copies of current tracked sessions.
func (m *Manager) SnapshotForManifest() []*tmux.Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*tmux.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s == nil || s.Name == tmux.ControlSessionName() {
			continue
		}
		out = append(out, deepCopySession(s))
	}
	return out
}

func deepCopySession(s *tmux.Session) *tmux.Session {
	if s == nil {
		return nil
	}
	copySession := *s
	if len(s.Windows) > 0 {
		copySession.Windows = make([]*tmux.Window, 0, len(s.Windows))
		for _, win := range s.Windows {
			if win == nil {
				continue
			}
			copyWin := *win
			if len(win.Panes) > 0 {
				copyWin.Panes = make([]*tmux.Pane, 0, len(win.Panes))
				for _, pane := range win.Panes {
					if pane == nil {
						continue
					}
					copyPane := *pane
					copyWin.Panes = append(copyWin.Panes, &copyPane)
				}
			}
			copySession.Windows = append(copySession.Windows, &copyWin)
		}
	}
	return &copySession
}
