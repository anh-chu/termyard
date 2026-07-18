//go:build !linux

package recovery

import "github.com/anh-chu/termyard/pkg/model"

func TuneOomPanes(sessions []*model.Session) error { return nil }
