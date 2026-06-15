package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Peer represents a known paired peer
type Peer struct {
	Name          string    `json:"name"`
	PublicKey     string    `json:"public_key"`
	PairedAt      time.Time `json:"paired_at"`
	Address       string    `json:"address"`             // host:port last successfully used
	Enabled       bool      `json:"enabled"`             // auto-reconnect on/off (governs outbound dials only)
	InitiatedByUs bool      `json:"initiated_by_us"`     // true ⇒ we dial; false ⇒ we wait for inbound
	LastSeen      time.Time `json:"last_seen,omitempty"` // updated on every successful connect
}

// Fingerprint returns a short identifier derived from the peer's public key
func (p *Peer) Fingerprint() string {
	id := &Identity{PublicKey: p.PublicKey}
	return id.Fingerprint()
}

// PeerStore manages the list of known peers
type PeerStore struct {
	mu    sync.RWMutex
	path  string
	store peerStoreData
}

type peerStoreData struct {
	Peers []Peer `json:"peers"`
}

// NewPeerStore loads or creates the peer store. Performs a one-time migration
// for legacy records: missing Enabled ⇒ true, missing InitiatedByUs ⇒ true.
func NewPeerStore() (*PeerStore, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}

	path := filepath.Join(dir, "peers.json")
	ps := &PeerStore{path: path}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ps, nil
		}
		return nil, fmt.Errorf("read peers: %w", err)
	}

	// Detect which fields are present using a raw map per peer.
	var raw struct {
		Peers []map[string]any `json:"peers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse peers (raw): %w", err)
	}

	if err := json.Unmarshal(data, &ps.store); err != nil {
		return nil, fmt.Errorf("parse peers: %w", err)
	}

	// Migrate missing fields with sensible defaults.
	mutated := false
	for i := range ps.store.Peers {
		var rawMap map[string]any
		if i < len(raw.Peers) {
			rawMap = raw.Peers[i]
		}
		if _, ok := rawMap["enabled"]; !ok {
			ps.store.Peers[i].Enabled = true
			mutated = true
		}
		if _, ok := rawMap["initiated_by_us"]; !ok {
			ps.store.Peers[i].InitiatedByUs = true
			mutated = true
		}
	}
	if mutated {
		ps.mu.Lock()
		err := ps.save()
		ps.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("migrate peers: %w", err)
		}
	}

	return ps, nil
}

// Add adds (or refreshes) a peer keyed by public key.
func (ps *PeerStore) Add(peer Peer) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for i, p := range ps.store.Peers {
		if p.PublicKey == peer.PublicKey {
			ps.store.Peers[i] = peer
			return ps.save()
		}
	}

	ps.store.Peers = append(ps.store.Peers, peer)
	return ps.save()
}

// RemoveByPublicKey removes a peer by public key.
func (ps *PeerStore) RemoveByPublicKey(publicKey string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	for i, p := range ps.store.Peers {
		if p.PublicKey == publicKey {
			ps.store.Peers = append(ps.store.Peers[:i], ps.store.Peers[i+1:]...)
			return ps.save()
		}
	}
	return fmt.Errorf("peer not found")
}

// Get returns a peer by name.
func (ps *PeerStore) Get(name string) *Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	for _, p := range ps.store.Peers {
		if p.Name == name {
			cp := p
			return &cp
		}
	}
	return nil
}

// GetByPublicKey returns a peer by public key.
func (ps *PeerStore) GetByPublicKey(publicKey string) *Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	for _, p := range ps.store.Peers {
		if p.PublicKey == publicKey {
			cp := p
			return &cp
		}
	}
	return nil
}

// GetByFingerprint returns a peer by short fingerprint.
func (ps *PeerStore) GetByFingerprint(fp string) *Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	for _, p := range ps.store.Peers {
		if p.Fingerprint() == fp {
			cp := p
			return &cp
		}
	}
	return nil
}

// List returns all known peers.
func (ps *PeerStore) List() []Peer {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	result := make([]Peer, len(ps.store.Peers))
	copy(result, ps.store.Peers)
	return result
}

// UpdateAddress updates the last-known address used to reach a peer.
func (ps *PeerStore) UpdateAddress(publicKey, address string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i, p := range ps.store.Peers {
		if p.PublicKey == publicKey {
			ps.store.Peers[i].Address = address
			return ps.save()
		}
	}
	return fmt.Errorf("peer not found")
}

// SetEnabled toggles auto-reconnect for a peer.
func (ps *PeerStore) SetEnabled(publicKey string, enabled bool) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i, p := range ps.store.Peers {
		if p.PublicKey == publicKey {
			ps.store.Peers[i].Enabled = enabled
			return ps.save()
		}
	}
	return fmt.Errorf("peer not found")
}

// SetInitiatedByUs flips the dialer/listener role for a peer.
func (ps *PeerStore) SetInitiatedByUs(publicKey string, initiated bool) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i, p := range ps.store.Peers {
		if p.PublicKey == publicKey {
			ps.store.Peers[i].InitiatedByUs = initiated
			return ps.save()
		}
	}
	return fmt.Errorf("peer not found")
}

// UpdateLastSeen sets LastSeen to now.
func (ps *PeerStore) UpdateLastSeen(publicKey string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for i, p := range ps.store.Peers {
		if p.PublicKey == publicKey {
			ps.store.Peers[i].LastSeen = time.Now()
			return ps.save()
		}
	}
	return fmt.Errorf("peer not found")
}

// save writes the peer store to disk (must be called with lock held)
func (ps *PeerStore) save() error {
	data, err := json.MarshalIndent(ps.store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal peers: %w", err)
	}
	if err := os.WriteFile(ps.path, data, 0600); err != nil {
		return fmt.Errorf("write peers: %w", err)
	}
	return nil
}
