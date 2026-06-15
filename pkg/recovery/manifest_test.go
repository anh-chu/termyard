package recovery

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := &Manifest{
		Version:    CurrentVersion,
		UpdatedAt:  time.Unix(123, 0).UTC(),
		Generation: 7,
		Sessions: []SessionSnapshot{{
			Name:           "s1",
			ProjectPath:    "/tmp/project",
			AgentType:      "claude",
			AgentSessionID: "abc",
			Windows: []WindowSnapshot{{
				Index:  0,
				Name:   "main",
				Layout: "even-horizontal",
				Panes:  []PaneSnapshot{{Index: 0, CWD: "/tmp/project", StartCommand: "claude --resume abc"}},
			}},
		}},
	}
	if err := m.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if !reflect.DeepEqual(m, got) {
		t.Fatalf("round trip mismatch\n got=%#v\nwant=%#v", got, m)
	}
}

func TestAtomicWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := &Manifest{Version: CurrentVersion, Sessions: []SessionSnapshot{{Name: "s1"}}}
	if err := m.Save(); err != nil {
		t.Fatalf("first Save() failed: %v", err)
	}
	if err := m.Save(); err != nil {
		t.Fatalf("second Save() failed: %v", err)
	}

	path, err := ManifestPath()
	if err != nil {
		t.Fatalf("ManifestPath() failed: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file still present: %v", err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := ManifestPath()
	if err != nil {
		t.Fatalf("ManifestPath() failed: %v", err)
	}
	_ = os.Remove(path)
	_ = os.Remove(filepath.Dir(path))

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if got.Version != CurrentVersion || len(got.Sessions) != 0 {
		t.Fatalf("unexpected empty manifest: %#v", got)
	}
}
