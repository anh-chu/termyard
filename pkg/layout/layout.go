package layout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Layout is an opaque blob of UI viewport state synced across all clients of
// a single guppi node. The server treats it as a JSON object and does not
// interpret its structure — keys are owned by the frontend.
//
// Convention (frontend-defined, kept here for reference):
//   - pane_tree:        the active split tree (or null)
//   - active_key:       session key of the focused pane
//   - single_view:      key of the single-view session if not split
//   - saved_groups:     array of {id, tree, activeKey, name}
//   - active_group_id:  id of the active group
//   - active_group_name string
//   - group_order:      array of group ids
//   - sidebar_collapsed: bool
//   - collapsed_groups: array of group ids that are collapsed in the sidebar
type Layout struct {
	Data      map[string]json.RawMessage `json:"data"`
	UpdatedAt time.Time                  `json:"updated_at"`
	// UpdatedBy is the client ID of the tab that last wrote. Used by other
	// tabs to ignore their own echoes.
	UpdatedBy string `json:"updated_by,omitempty"`
}

// Store persists the layout to disk.
type Store struct {
	mu   sync.RWMutex
	path string
	data Layout
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "guppi"), nil
}

// NewStore loads or creates the layout store.
func NewStore() (*Store, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		path: filepath.Join(dir, "layout.json"),
		data: Layout{Data: map[string]json.RawMessage{}},
	}
	if raw, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(raw, &s.data)
		if s.data.Data == nil {
			s.data.Data = map[string]json.RawMessage{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

// Get returns a snapshot of the current layout.
func (s *Store) Get() Layout {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := Layout{
		Data:      make(map[string]json.RawMessage, len(s.data.Data)),
		UpdatedAt: s.data.UpdatedAt,
		UpdatedBy: s.data.UpdatedBy,
	}
	for k, v := range s.data.Data {
		cp.Data[k] = v
	}
	return cp
}

// Set replaces the layout payload and persists. UpdatedAt is set to now.
func (s *Store) Set(data map[string]json.RawMessage, clientID string) (Layout, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Data = data
	s.data.UpdatedAt = time.Now()
	s.data.UpdatedBy = clientID
	if err := s.save(); err != nil {
		return Layout{}, err
	}
	return s.snapshot(), nil
}

// ApplyRemote applies a remote layout update using last-write-wins on
// UpdatedAt. Returns the resulting snapshot and whether the update was
// accepted (newer than the local copy). Origin is stored in UpdatedBy so
// the next /api/layout broadcast carries it through to all browsers.
func (s *Store) ApplyRemote(data map[string]json.RawMessage, updatedAt time.Time, origin string) (Layout, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !updatedAt.After(s.data.UpdatedAt) {
		return s.snapshot(), false, nil
	}
	s.data.Data = data
	s.data.UpdatedAt = updatedAt
	s.data.UpdatedBy = origin
	if err := s.save(); err != nil {
		return Layout{}, false, err
	}
	return s.snapshot(), true, nil
}

func (s *Store) snapshot() Layout {
	cp := Layout{
		Data:      make(map[string]json.RawMessage, len(s.data.Data)),
		UpdatedAt: s.data.UpdatedAt,
		UpdatedBy: s.data.UpdatedBy,
	}
	for k, v := range s.data.Data {
		cp.Data[k] = v
	}
	return cp
}

func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}
