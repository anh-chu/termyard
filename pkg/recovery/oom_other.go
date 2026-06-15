//go:build !linux

package recovery

import "github.com/anh-chu/termyard/pkg/tmux"

func TuneOomPanes(sessions []*tmux.Session) error { return nil }
