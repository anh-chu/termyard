package sessionorder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anh-chu/termyard/pkg/config"
)

// Order is the stored rank for one session key.
type Order struct {
	Rank      string    `json:"rank"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store persists sidebar ordering by global session key.
type Store struct {
	mu     sync.RWMutex
	path   string
	orders map[string]Order
}

// NewStore loads or creates the session-order store.
func NewStore() (*Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		path:   filepath.Join(dir, "session-order.json"),
		orders: map[string]Order{},
	}
	if raw, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(raw, &s.orders)
		if s.orders == nil {
			s.orders = map[string]Order{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

// Snapshot returns a copy of all retained orders.
func (s *Store) Snapshot() map[string]Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]Order, len(s.orders))
	for k, v := range s.orders {
		out[k] = v
	}
	return out
}

// Ranks is the flat client-facing view of key -> rank.
func (s *Store) Ranks() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.orders))
	for k, v := range s.orders {
		if v.Rank != "" {
			out[k] = v.Rank
		}
	}
	return out
}

// Set applies a local update, stamping it with the current time.
func (s *Store) Set(key, rank string) (Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := Order{Rank: rank, UpdatedAt: time.Now()}
	s.orders[key] = cur
	if err := s.save(); err != nil {
		return Order{}, err
	}
	return cur, nil
}

// ApplyRemote merges a single-key delta via per-key LWW.
func (s *Store) ApplyRemote(key string, in Order) (Order, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.orders[key]; ok && !in.UpdatedAt.After(cur.UpdatedAt) {
		return cur, false, nil
	}
	s.orders[key] = in
	if err := s.save(); err != nil {
		return Order{}, false, err
	}
	return in, true, nil
}

// ApplySnapshot merges a full peer snapshot via per-key LWW.
func (s *Store) ApplySnapshot(snap map[string]Order) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var changed []string
	for k, in := range snap {
		if cur, ok := s.orders[k]; ok && !in.UpdatedAt.After(cur.UpdatedAt) {
			continue
		}
		s.orders[k] = in
		changed = append(changed, k)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return changed, nil
}

// Get returns the stored order for a key.
func (s *Store) Get(key string) Order {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.orders[key]
}

// MigrateKey moves rank from oldName to newName for keys owned by localHost.
func (s *Store) MigrateKey(localHost, oldName, newName string) ([]string, error) {
	if oldName == "" || newName == "" || oldName == newName {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	type move struct{ oldKey, newKey string }
	var moves []move
	for key := range s.orders {
		host, name := splitKey(key)
		if name != oldName || (host != "" && host != localHost) {
			continue
		}
		newKey := newName
		if host != "" {
			newKey = host + "/" + newName
		}
		if newKey != key {
			moves = append(moves, move{oldKey: key, newKey: newKey})
		}
	}
	if len(moves) == 0 {
		return nil, nil
	}

	migrated := make([]string, 0, len(moves))
	for _, mv := range moves {
		order := s.orders[mv.oldKey]
		order.UpdatedAt = time.Now()
		delete(s.orders, mv.oldKey)
		s.orders[mv.newKey] = order
		migrated = append(migrated, mv.newKey)
	}
	if err := s.save(); err != nil {
		return migrated, err
	}
	return migrated, nil
}

func (s *Store) save() error {
	return config.WriteJSON(s.path, s.orders, 0o644)
}

// ownerOf returns host prefix of a global session key.
func ownerOf(key string) string {
	host, _ := splitKey(key)
	return host
}

// splitKey divides a global session key into host prefix and session name.
func splitKey(key string) (host, name string) {
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return key[:i], key[i+1:]
		}
	}
	return "", key
}
