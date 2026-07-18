//go:build linux

package recovery

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/anh-chu/termyard/pkg/model"
)

func TestTuneOomPanesAtRecursesChildren(t *testing.T) {
	root := t.TempDir()
	mustProc := func(pid int) {
		path := filepath.Join(root, "proc", strconv.Itoa(pid))
		if err := os.MkdirAll(filepath.Join(path, "task", strconv.Itoa(pid)), 0o755); err != nil {
			t.Fatalf("MkdirAll() failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(path, "oom_score_adj"), []byte("0"), 0o644); err != nil {
			t.Fatalf("write oom file failed: %v", err)
		}
	}
	writeChildren := func(pid int, children string) {
		path := filepath.Join(root, "proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children")
		if err := os.WriteFile(path, []byte(children), 0o644); err != nil {
			t.Fatalf("write children failed: %v", err)
		}
	}

	mustProc(123)
	mustProc(456)
	mustProc(789)
	writeChildren(123, "456")
	writeChildren(456, "789")
	writeChildren(789, "")

	sessions := []*model.Session{{Windows: []*model.Window{{Panes: []*model.Pane{{PID: 123}}}}}}
	if err := tuneOomPanesAt(filepath.Join(root, "proc"), sessions); err != nil {
		t.Fatalf("tuneOomPanesAt() failed: %v", err)
	}
	for _, pid := range []int{123, 456, 789} {
		got, err := os.ReadFile(filepath.Join(root, "proc", strconv.Itoa(pid), "oom_score_adj"))
		if err != nil {
			t.Fatalf("ReadFile(%d) failed: %v", pid, err)
		}
		if string(got) != "300" {
			t.Fatalf("oom_score_adj[%d] = %q, want %q", pid, got, "300")
		}
	}
}
