package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeLegacyPeers(t *testing.T, dir string, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "peers.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPeerStoreMigratesLegacyFields(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	writeLegacyPeers(t, filepath.Join(tmpHome, ".config", "guppi"), `{
		"peers": [
			{"name":"old","public_key":"pk","paired_at":"2020-01-01T00:00:00Z","tls_cert_pem":"junk"}
		]
	}`)

	ps, err := NewPeerStore()
	if err != nil {
		t.Fatal(err)
	}
	peers := ps.List()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}
	if !peers[0].Enabled {
		t.Errorf("Enabled should default to true on migration")
	}
	if !peers[0].InitiatedByUs {
		t.Errorf("InitiatedByUs should default to true on migration")
	}

	// Verify the file was re-saved with the new fields.
	raw, err := os.ReadFile(filepath.Join(tmpHome, ".config", "guppi", "peers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Peers []map[string]any `json:"peers"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved.Peers[0]["enabled"]; !ok {
		t.Errorf("saved file missing 'enabled' field after migration")
	}
	if _, ok := saved.Peers[0]["initiated_by_us"]; !ok {
		t.Errorf("saved file missing 'initiated_by_us' field after migration")
	}
}

func TestPeerStoreRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	ps, err := NewPeerStore()
	if err != nil {
		t.Fatal(err)
	}
	p := Peer{
		Name:          "n",
		PublicKey:     "pk",
		Address:       "host:7654",
		Enabled:       true,
		InitiatedByUs: true,
	}
	if err := ps.Add(p); err != nil {
		t.Fatal(err)
	}
	if got := ps.GetByPublicKey("pk"); got == nil || got.Name != "n" {
		t.Errorf("GetByPublicKey: %+v", got)
	}
	if err := ps.SetEnabled("pk", false); err != nil {
		t.Fatal(err)
	}
	if got := ps.GetByPublicKey("pk"); got.Enabled {
		t.Errorf("SetEnabled did not persist")
	}
	if err := ps.SetInitiatedByUs("pk", false); err != nil {
		t.Fatal(err)
	}
	if got := ps.GetByPublicKey("pk"); got.InitiatedByUs {
		t.Errorf("SetInitiatedByUs did not persist")
	}
	if err := ps.UpdateAddress("pk", "host:9999"); err != nil {
		t.Fatal(err)
	}
	if got := ps.GetByPublicKey("pk"); got.Address != "host:9999" {
		t.Errorf("UpdateAddress did not persist: %+v", got)
	}
	if err := ps.RemoveByPublicKey("pk"); err != nil {
		t.Fatal(err)
	}
	if got := ps.GetByPublicKey("pk"); got != nil {
		t.Errorf("RemoveByPublicKey left record: %+v", got)
	}
}

func TestPeerStoreGetByFingerprint(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	ps, _ := NewPeerStore()
	id, _ := Generate("n")
	p := Peer{Name: "n", PublicKey: id.PublicKey, Enabled: true}
	if err := ps.Add(p); err != nil {
		t.Fatal(err)
	}
	if got := ps.GetByFingerprint(id.Fingerprint()); got == nil {
		t.Fatalf("GetByFingerprint nil")
	}
}
