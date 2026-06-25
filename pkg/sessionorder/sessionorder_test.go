package sessionorder

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestApplyRemoteAndSnapshotAndMigrate(t *testing.T) {
	t.Run("ApplyRemote LWW", func(t *testing.T) {
		s := &Store{path: filepath.Join(t.TempDir(), "session-order.json"), orders: map[string]Order{
			"host/session-a": {Rank: "a", UpdatedAt: time.Unix(10, 0)},
		}}

		cases := []struct {
			name      string
			in        Order
			accepted  bool
			wantRank  string
			wantStamp time.Time
		}{
			{name: "older", in: Order{Rank: "older", UpdatedAt: time.Unix(9, 0)}, accepted: false, wantRank: "a", wantStamp: time.Unix(10, 0)},
			{name: "equal", in: Order{Rank: "equal", UpdatedAt: time.Unix(10, 0)}, accepted: false, wantRank: "a", wantStamp: time.Unix(10, 0)},
			{name: "newer", in: Order{Rank: "new", UpdatedAt: time.Unix(11, 0)}, accepted: true, wantRank: "new", wantStamp: time.Unix(11, 0)},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, ok, err := s.ApplyRemote("host/session-a", tc.in)
				if err != nil {
					t.Fatalf("ApplyRemote err: %v", err)
				}
				if ok != tc.accepted {
					t.Fatalf("accepted = %v, want %v", ok, tc.accepted)
				}
				if got.Rank != tc.wantRank || !got.UpdatedAt.Equal(tc.wantStamp) {
					t.Fatalf("got = %#v", got)
				}
			})
		}
	})

	t.Run("ApplySnapshot merges per key", func(t *testing.T) {
		s := &Store{path: filepath.Join(t.TempDir(), "session-order.json"), orders: map[string]Order{
			"keep":  {Rank: "k", UpdatedAt: time.Unix(20, 0)},
			"stale": {Rank: "old", UpdatedAt: time.Unix(20, 0)},
		}}
		changed, err := s.ApplySnapshot(map[string]Order{
			"keep":  {Rank: "old", UpdatedAt: time.Unix(19, 0)},
			"stale": {Rank: "new", UpdatedAt: time.Unix(21, 0)},
			"add":   {Rank: "x", UpdatedAt: time.Unix(22, 0)},
		})
		if err != nil {
			t.Fatalf("ApplySnapshot err: %v", err)
		}
		sort.Strings(changed)
		if !reflect.DeepEqual(changed, []string{"add", "stale"}) {
			t.Fatalf("changed = %#v", changed)
		}
		if got := s.Get("keep"); got.Rank != "k" || !got.UpdatedAt.Equal(time.Unix(20, 0)) {
			t.Fatalf("keep = %#v", got)
		}
		if got := s.Get("stale"); got.Rank != "new" || !got.UpdatedAt.Equal(time.Unix(21, 0)) {
			t.Fatalf("stale = %#v", got)
		}
		if got := s.Get("add"); got.Rank != "x" || !got.UpdatedAt.Equal(time.Unix(22, 0)) {
			t.Fatalf("add = %#v", got)
		}
	})

	t.Run("MigrateKey local only", func(t *testing.T) {
		s := &Store{path: filepath.Join(t.TempDir(), "session-order.json"), orders: map[string]Order{
			"local-fp/old": {Rank: "local", UpdatedAt: time.Unix(10, 0)},
			"peer-fp/old":  {Rank: "peer", UpdatedAt: time.Unix(11, 0)},
		}}
		migrated, err := s.MigrateKey("local-fp", "old", "new")
		if err != nil {
			t.Fatalf("MigrateKey err: %v", err)
		}
		if !reflect.DeepEqual(migrated, []string{"local-fp/new"}) {
			t.Fatalf("migrated = %#v", migrated)
		}
		if _, ok := s.orders["local-fp/old"]; ok {
			t.Fatal("local old key still present")
		}
		if got := s.orders["local-fp/new"]; got.Rank != "local" || got.UpdatedAt.IsZero() {
			t.Fatalf("local new = %#v", got)
		}
		if got := s.orders["peer-fp/old"]; got.Rank != "peer" {
			t.Fatalf("peer key mutated = %#v", got)
		}
	})
}

func TestSetGetAndPersistence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s1, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore err: %v", err)
	}
	gotSet, err := s1.Set("host/session-a", "rank-1")
	if err != nil {
		t.Fatalf("Set err: %v", err)
	}
	if got := s1.Get("host/session-a"); got != gotSet {
		t.Fatalf("Get = %#v, want %#v", got, gotSet)
	}
	if got := s1.Ranks(); got["host/session-a"] != "rank-1" {
		t.Fatalf("Ranks = %#v", got)
	}

	s2, err := NewStore()
	if err != nil {
		t.Fatalf("reload NewStore err: %v", err)
	}
	if got := s2.Get("host/session-a"); got.Rank != gotSet.Rank || !got.UpdatedAt.Equal(gotSet.UpdatedAt) {
		t.Fatalf("reloaded = %#v, want %#v", got, gotSet)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "termyard", "session-order.json")); err != nil {
		t.Fatalf("session-order.json missing: %v", err)
	}
}
