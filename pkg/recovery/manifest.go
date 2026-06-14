package recovery

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const CurrentVersion = 1

// PaneSnapshot is manifest view of one tmux pane.
type PaneSnapshot struct {
	ID             string `json:"id"`
	Index          int    `json:"index"`
	Active         bool   `json:"active"`
	CWD            string `json:"cwd"`
	StartCommand   string `json:"start_command"`
	CurrentCommand string `json:"current_command"`
	AgentType      string `json:"agent_type,omitempty"`
}

// WindowSnapshot is manifest view of one tmux window.
type WindowSnapshot struct {
	Index  int            `json:"index"`
	Name   string         `json:"name"`
	Active bool           `json:"active"`
	Layout string         `json:"layout"`
	Panes  []PaneSnapshot `json:"panes"`
}

// SessionSnapshot is manifest view of one tmux session.
type SessionSnapshot struct {
	Name           string           `json:"name"`
	ProjectPath    string           `json:"project_path,omitempty"`
	AgentType      string           `json:"agent_type,omitempty"`
	AgentSessionID string           `json:"agent_session_id,omitempty"`
	Windows        []WindowSnapshot `json:"windows"`
}

// Manifest stores crash-recovery snapshot data.
type Manifest struct {
	Version    int               `json:"version"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Generation uint64            `json:"generation"`
	Sessions   []SessionSnapshot `json:"sessions"`
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "guppi"), nil
}

// ManifestPath returns manifest file path.
func ManifestPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session-manifest.json"), nil
}

// Load reads manifest from disk.
func Load() (*Manifest, error) {
	path, err := ManifestPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Version: CurrentVersion}, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Version == 0 {
		m.Version = CurrentVersion
	}
	return &m, nil
}

// ForgetSession removes a session from the persisted manifest synchronously.
//
// Call this on an intentional kill so the crash-recovery rebuilder cannot
// resurrect it. The periodic Snapshotter would eventually drop it (~8s), but a
// faster recovery probe (or a last-session kill that takes the tmux server down
// with it) can rebuild from a stale manifest before that. Removing it here
// closes that race.
func ForgetSession(name string) error {
	if name == "" {
		return nil
	}
	m, err := Load()
	if err != nil {
		return err
	}
	if m == nil || len(m.Sessions) == 0 {
		return nil
	}
	filtered := m.Sessions[:0]
	removed := false
	for _, s := range m.Sessions {
		if s.Name == name {
			removed = true
			continue
		}
		filtered = append(filtered, s)
	}
	if !removed {
		return nil
	}
	m.Sessions = filtered
	m.Generation++
	m.UpdatedAt = time.Now()
	return m.Save()
}

// Save writes manifest atomically.
func (m *Manifest) Save() error {
	if m.Version == 0 {
		m.Version = CurrentVersion
	}
	path, err := ManifestPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

type manifestFingerprint struct {
	Version  int               `json:"version"`
	Sessions []SessionSnapshot `json:"sessions"`
}

func (m *Manifest) fingerprint() string {
	if m == nil {
		return ""
	}
	view := manifestFingerprint{Version: m.Version, Sessions: m.Sessions}
	data, err := json.Marshal(view)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}
