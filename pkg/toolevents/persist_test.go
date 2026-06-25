package toolevents

import (
	"path/filepath"
	"testing"
)

// TestPersistRoundTrip verifies retained waiting events and session metadata
// survive a "restart": a fresh tracker loading the same file sees them again.
func TestPersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toolevents.json")

	tr := NewTracker()
	tr.path = path
	tr.Record(&Event{
		Tool:       ToolClaude,
		Status:     StatusWaiting,
		Session:    "work",
		Window:     0,
		Pane:       "%1",
		Message:    "needs approval",
		UserPrompt: "fix the bug",
	})

	// Simulate restart: new tracker, same file.
	tr2 := NewTracker()
	tr2.path = path
	tr2.load()

	got := tr2.RetainedWaitingForPane("%1")
	if got == nil {
		t.Fatal("waiting event not restored after reload")
	}
	if got.Message != "needs approval" {
		t.Fatalf("message = %q, want %q", got.Message, "needs approval")
	}

	meta, ok := tr2.sessionMeta["\x00work"]
	if !ok || meta.UserPrompt != "fix the bug" {
		t.Fatalf("session meta not restored: %+v (ok=%v)", meta, ok)
	}
}
