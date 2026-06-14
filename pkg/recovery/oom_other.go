//go:build !linux

package recovery

import "github.com/ekristen/guppi/pkg/tmux"

func TuneOomPanes(sessions []*tmux.Session) error { return nil }
