package groupsync

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func mustTime(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

func TestApplyRemoteFieldLWW(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "groups.json"), groups: map[string]Group{
		"g1": {
			Tree:          json.RawMessage(`{"type":"leaf","sessionKey":"a"}`),
			TreeUpdatedAt: mustTime(10),
			Name:          "old",
			NameUpdatedAt: mustTime(10),
			Rank:          "r1",
			RankUpdatedAt: mustTime(10),
		},
	}}

	got, ok, err := s.ApplyRemote("g1", Group{
		Tree:          json.RawMessage(`{"type":"leaf","sessionKey":"b"}`),
		TreeUpdatedAt: mustTime(5),
		Name:          "new",
		NameUpdatedAt: mustTime(20),
		Rank:          "r2",
		RankUpdatedAt: mustTime(5),
	})
	if err != nil {
		t.Fatalf("ApplyRemote: %v", err)
	}
	if !ok {
		t.Fatal("expected accepted")
	}
	if !bytes.Equal(got.Tree, []byte(`{"type":"leaf","sessionKey":"a"}`)) {
		t.Fatalf("tree = %s", got.Tree)
	}
	if got.Name != "new" || got.Rank != "r1" {
		t.Fatalf("merged = %#v", got)
	}
	if got.NameUpdatedAt != mustTime(20) || got.TreeUpdatedAt != mustTime(10) || got.RankUpdatedAt != mustTime(10) {
		t.Fatalf("clocks = %#v", got)
	}
}

func TestDeleteTombstoneStaysDeletedAgainstStaleSnapshot(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "groups.json"), groups: map[string]Group{
		"g1": {
			Tree:          json.RawMessage(`{"type":"leaf","sessionKey":"a"}`),
			TreeUpdatedAt: mustTime(10),
			Name:          "gone",
			NameUpdatedAt: mustTime(10),
			Rank:          "r1",
			RankUpdatedAt: mustTime(10),
			DeletedAt:     mustTime(20),
		},
	}}

	got, ok, err := s.ApplyRemote("g1", Group{
		Tree:          json.RawMessage(`{"type":"leaf","sessionKey":"a"}`),
		TreeUpdatedAt: mustTime(5),
		Name:          "old",
		NameUpdatedAt: mustTime(5),
		Rank:          "r0",
		RankUpdatedAt: mustTime(5),
		DeletedAt:     time.Time{},
	})
	if err != nil {
		t.Fatalf("ApplyRemote: %v", err)
	}
	if ok {
		t.Fatal("stale live snapshot should not win")
	}
	if got.DeletedAt != mustTime(20) {
		t.Fatalf("deleted_at = %v", got.DeletedAt)
	}
	if live := s.Live(); len(live) != 0 {
		t.Fatalf("live = %#v", live)
	}
}

func TestMigrateKeyRewritesOwnedLeavesOnly(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "groups.json"), groups: map[string]Group{
		"g1": {
			Tree:          json.RawMessage(`{"type":"split","direction":"h","ratio":0.5,"first":{"type":"leaf","sessionKey":"local-fp/old"},"second":{"type":"split","direction":"v","ratio":0.5,"first":{"type":"leaf","sessionKey":"peer-fp/old"},"second":{"type":"leaf","sessionKey":"old"}}}`),
			TreeUpdatedAt: mustTime(10),
		},
	}}

	changed, err := s.MigrateKey("local-fp", "old", "new")
	if err != nil {
		t.Fatalf("MigrateKey: %v", err)
	}
	if len(changed) != 1 || changed[0] != "g1" {
		t.Fatalf("changed = %#v", changed)
	}
	got := s.groups["g1"]
	want := `{"type":"split","direction":"h","ratio":0.5,"first":{"type":"leaf","sessionKey":"local-fp/new"},"second":{"type":"split","direction":"v","ratio":0.5,"first":{"type":"leaf","sessionKey":"peer-fp/old"},"second":{"type":"leaf","sessionKey":"new"}}}`
	var gotTree any
	var wantTree any
	if err := json.Unmarshal(got.Tree, &gotTree); err != nil {
		t.Fatalf("got tree unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(want), &wantTree); err != nil {
		t.Fatalf("want tree unmarshal: %v", err)
	}
	if !reflect.DeepEqual(gotTree, wantTree) {
		t.Fatalf("tree = %#v", gotTree)
	}
	if !got.TreeUpdatedAt.After(mustTime(10)) {
		t.Fatalf("tree clock not bumped: %v", got.TreeUpdatedAt)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	var err error
	if _, err = s.SetTree("g1", json.RawMessage(`{"type":"leaf","sessionKey":"x"}`)); err != nil {
		t.Fatalf("SetTree: %v", err)
	}
	if _, err = s.SetName("g1", "name"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if _, err = s.SetRank("g1", "rank"); err != nil {
		t.Fatalf("SetRank: %v", err)
	}
	if _, err = s.Delete("g1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	reloaded, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	got, ok := reloaded.Get("g1")
	if !ok {
		t.Fatal("missing reloaded group")
	}
	if got.Name != "name" || got.Rank != "rank" || got.DeletedAt.IsZero() {
		t.Fatalf("reloaded = %#v", got)
	}
	if len(reloaded.Live()) != 0 {
		t.Fatalf("live = %#v", reloaded.Live())
	}
}
