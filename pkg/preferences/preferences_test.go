package preferences

import (
	"os"
	"path/filepath"
	"testing"
)

// TestUpdateDoesNotAliasCallerStruct reproduces the API key masking corruption:
// the handler called Update(&prefs) then masked prefs.AINaming.APIKey for the
// HTTP echo. When Update kept the caller's pointer, that mask leaked into the
// store and the next save's "restore from store" persisted the mask, clobbering
// the real key.
func TestUpdateDoesNotAliasCallerStruct(t *testing.T) {
	dir := t.TempDir()
	s := &Store{path: filepath.Join(dir, "preferences.json"), data: Default()}

	prefs := Default()
	prefs.AINaming.Enabled = true
	prefs.AINaming.Endpoint = "http://example/v1"
	prefs.AINaming.APIKey = "sk-real-secret"
	if err := s.Update(prefs); err != nil {
		t.Fatal(err)
	}

	// Simulate the handler mutating its local copy for the masked echo.
	prefs.AINaming.APIKey = APIKeyMask

	// Store must still hold the real key, not the mask.
	if got := s.Get().AINaming.APIKey; got != "sk-real-secret" {
		t.Fatalf("store key corrupted by caller mutation: got %q", got)
	}

	// And it must have been written to disk as the real key.
	raw, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || contains(raw, APIKeyMask) {
		t.Fatalf("mask leaked to disk: %s", raw)
	}
}

func contains(b []byte, sub string) bool {
	return len(sub) > 0 && len(b) >= len(sub) && indexOf(string(b), sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
