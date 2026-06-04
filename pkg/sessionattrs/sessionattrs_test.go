package sessionattrs

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{
		path:  filepath.Join(t.TempDir(), "session-attrs.json"),
		attrs: map[string]Attr{},
	}
}

func TestSetAndSets(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Set("fpA/foo", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set("fpA/bar", false, true); err != nil {
		t.Fatal(err)
	}
	sets := s.Sets()
	if len(sets.Background) != 1 || sets.Background[0] != "fpA/foo" {
		t.Fatalf("background = %v", sets.Background)
	}
	if len(sets.Hidden) != 1 || sets.Hidden[0] != "fpA/bar" {
		t.Fatalf("hidden = %v", sets.Hidden)
	}
}

func TestApplyRemoteLWW(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if _, err := s.Set("fpA/foo", true, false); err != nil {
		t.Fatal(err)
	}
	// Older delta must be rejected.
	_, accepted, err := s.ApplyRemote("fpA/foo", Attr{Background: false, UpdatedAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("stale delta should be rejected")
	}
	if !s.Get("fpA/foo").Background {
		t.Fatal("background should survive stale delta")
	}
	// Newer delta wins.
	_, accepted, err = s.ApplyRemote("fpA/foo", Attr{Background: false, UpdatedAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !accepted {
		t.Fatal("newer delta should be accepted")
	}
	if s.Get("fpA/foo").Background {
		t.Fatal("background should be cleared by newer delta")
	}
}

func TestPruneOnlyOnlineOwner(t *testing.T) {
	s := newTestStore(t)
	// fpA owns foo (will go missing), fpB owns bar (owner offline).
	if _, err := s.Set("fpA/foo", true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set("fpB/bar", true, false); err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{} // both sessions absent
	online := map[string]bool{"fpA": true} // only fpA online

	gone, changed, err := s.Prune(live, online)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a change")
	}
	if len(gone) != 1 || gone[0] != "fpA/foo" {
		t.Fatalf("gone = %v; want [fpA/foo]", gone)
	}
	// fpA/foo is gone (online owner, absent session); fpB/bar survives (offline owner).
	sets := s.Sets()
	if len(sets.Background) != 1 || sets.Background[0] != "fpB/bar" {
		t.Fatalf("background = %v; want [fpB/bar]", sets.Background)
	}
	// The gone key is a tombstone, not deleted, so a stale delta can't resurrect it.
	if tomb := s.Get("fpA/foo"); !tomb.empty() {
		t.Fatal("fpA/foo should be a tombstone")
	}
}

func TestPruneKeepsLiveSession(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Set("fpA/foo", true, false); err != nil {
		t.Fatal(err)
	}
	live := map[string]bool{"fpA/foo": true}
	online := map[string]bool{"fpA": true}
	gone, _, err := s.Prune(live, online)
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 0 {
		t.Fatalf("gone = %v; want none", gone)
	}
}

func TestApplySnapshotMerge(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	if _, err := s.Set("fpA/foo", true, false); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ApplySnapshot(map[string]Attr{
		"fpA/foo": {Background: false, UpdatedAt: now.Add(-time.Hour)}, // stale, ignored
		"fpB/bar": {Hidden: true, UpdatedAt: now},                      // new, accepted
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != "fpB/bar" {
		t.Fatalf("changed = %v; want [fpB/bar]", changed)
	}
	if !s.Get("fpA/foo").Background {
		t.Fatal("stale snapshot entry should not clobber fpA/foo")
	}
	if !s.Get("fpB/bar").Hidden {
		t.Fatal("fpB/bar should be hidden")
	}
}
