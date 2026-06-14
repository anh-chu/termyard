//go:build linux

package recovery

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ekristen/guppi/pkg/tmux"
)

func TuneOomPanes(sessions []*tmux.Session) error {
	return tuneOomPanesAt("/proc", sessions)
}

func tuneOomPanesAt(procRoot string, sessions []*tmux.Session) error {
	seen := make(map[int]struct{})
	for _, session := range sessions {
		if session == nil {
			continue
		}
		for _, win := range session.Windows {
			if win == nil {
				continue
			}
			for _, pane := range win.Panes {
				if pane == nil || pane.PID <= 0 {
					continue
				}
				_ = tuneProcessTreeOom(procRoot, pane.PID, seen)
			}
		}
	}
	return nil
}

func tuneProcessTreeOom(procRoot string, pid int, seen map[int]struct{}) error {
	if pid <= 0 {
		return nil
	}
	if _, ok := seen[pid]; ok {
		return nil
	}
	seen[pid] = struct{}{}

	adjPath := filepath.Join(procRoot, strconv.Itoa(pid), "oom_score_adj")
	_ = os.WriteFile(adjPath, []byte("300"), 0o644)

	childrenPath := filepath.Join(procRoot, strconv.Itoa(pid), "task", strconv.Itoa(pid), "children")
	raw, err := os.ReadFile(childrenPath)
	if err != nil {
		return nil
	}
	for _, field := range strings.Fields(string(raw)) {
		childPID, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		_ = tuneProcessTreeOom(procRoot, childPID, seen)
	}
	return nil
}
