package sessionattrs

import (
	"path/filepath"
	"testing"
)

func TestSetsIncludesScheduleIDs(t *testing.T) {
	s := &Store{attrs: map[string]Attr{
		"host-1/session-a": {
			Background: true,
			ScheduleID: "sched-123",
		},
	}}

	got := s.Sets()
	if len(got.Background) != 1 || got.Background[0] != "host-1/session-a" || len(got.Hidden) != 0 {
		t.Fatalf("sets = %#v", got)
	}
	if got.ScheduleIDs["host-1/session-a"] != "sched-123" {
		t.Fatalf("schedule ids = %#v", got.ScheduleIDs)
	}
}

func TestMigrateKeyPreservesAttrsAcrossRename(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "attrs.json"), attrs: map[string]Attr{
		"local-fp/old":  {Background: true, ScheduleID: "sched-1"},
		"bare-old":      {Hidden: true, ScheduleID: "sched-2"},
		"local-fp/keep": {ScheduleID: "sched-3"},
		"peer-fp/old":   {ScheduleID: "peer-sched"},
	}}

	// Local host-qualified rename keeps the host prefix.
	migrated, err := s.MigrateKey("local-fp", "old", "new")
	if err != nil {
		t.Fatalf("MigrateKey err: %v", err)
	}
	if len(migrated) != 1 || migrated[0] != "local-fp/new" {
		t.Fatalf("migrated = %#v", migrated)
	}
	if _, ok := s.attrs["local-fp/old"]; ok {
		t.Fatal("old key still present")
	}
	if got := s.attrs["local-fp/new"]; !got.Background || got.ScheduleID != "sched-1" {
		t.Fatalf("migrated attr = %#v", got)
	}

	// A peer-owned session with the same name must NOT be touched.
	if _, ok := s.attrs["peer-fp/old"]; !ok {
		t.Fatal("peer-owned key was wrongly migrated")
	}

	// Bare (single-host) key migrates without a prefix.
	migrated, _ = s.MigrateKey("local-fp", "bare-old", "bare-new")
	if len(migrated) != 1 || migrated[0] != "bare-new" {
		t.Fatalf("bare migrated = %#v", migrated)
	}
	if got := s.attrs["bare-new"]; !got.Hidden || got.ScheduleID != "sched-2" {
		t.Fatalf("bare attr = %#v", got)
	}

	// Unrelated keys untouched.
	if s.attrs["local-fp/keep"].ScheduleID != "sched-3" {
		t.Fatal("unrelated key mutated")
	}

	// No-op cases.
	if m, _ := s.MigrateKey("local-fp", "missing", "x"); m != nil {
		t.Fatal("expected nil for missing source")
	}
	if m, _ := s.MigrateKey("local-fp", "same", "same"); m != nil {
		t.Fatal("expected nil for identical names")
	}
}
