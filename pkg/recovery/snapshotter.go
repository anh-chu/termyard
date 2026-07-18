package recovery

import (
	"context"
	"sort"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/sessionattrs"
	"github.com/anh-chu/termyard/pkg/state"
)

// Snapshotter periodically persists manifest state.
type Snapshotter struct {
	stateMgr *state.Manager
	attrs    *sessionattrs.Store
	interval time.Duration
	lastFP   string
	gen      uint64
	log      *logrus.Entry
}

func NewSnapshotter(sm *state.Manager, attrs *sessionattrs.Store) *Snapshotter {
	return &Snapshotter{
		stateMgr: sm,
		attrs:    attrs,
		interval: 8 * time.Second,
		log:      logrus.WithField("component", "recovery-snapshot"),
	}
}

func (s *Snapshotter) Run(ctx context.Context) {
	if s == nil {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	if err := s.snapshotOnce(); err != nil {
		s.log.WithError(err).Warn("initial snapshot failed")
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.snapshotOnce(); err != nil {
				s.log.WithError(err).Warn("snapshot failed")
			}
		}
	}
}

func (s *Snapshotter) snapshotOnce() error {
	if s == nil || s.stateMgr == nil {
		return nil
	}
	manifest := &Manifest{Version: CurrentVersion}
	sessions := s.stateMgr.SnapshotForManifest()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].Name < sessions[j].Name })
	for _, session := range sessions {
		if session == nil {
			continue
		}
		snap := SessionSnapshot{
			Name:           session.Name,
			ProjectPath:    session.ProjectPath,
			AgentType:      session.AgentType,
			AgentSessionID: session.AgentSessionID,
		}
		// Prefer the intrinsic id carried on the session; fall back to the side-store.
		if snap.ScheduleID = session.ScheduleID; snap.ScheduleID == "" && s.attrs != nil {
			snap.ScheduleID = s.attrs.Get(session.Name).ScheduleID
		}
		windows := session.Windows
		if len(windows) > 0 {
			sort.Slice(windows, func(i, j int) bool { return windows[i].Index < windows[j].Index })
			for _, win := range windows {
				if win == nil {
					continue
				}
				ws := WindowSnapshot{Index: win.Index, Name: win.Name, Active: win.Active, Layout: win.Layout}
				panes := win.Panes
				if len(panes) > 0 {
					sort.Slice(panes, func(i, j int) bool { return panes[i].Index < panes[j].Index })
					for _, pane := range panes {
						if pane == nil {
							continue
						}
						ws.Panes = append(ws.Panes, PaneSnapshot{
							ID:             pane.ID,
							Index:          pane.Index,
							Active:         pane.Active,
							CWD:            pane.CurrentPath,
							StartCommand:   buildStartCommand(session.AgentType, session.AgentSessionID, pane.CurrentPath, pane.CurrentCommand),
							CurrentCommand: pane.CurrentCommand,
							AgentType:      session.AgentType,
						})
					}
				}
				snap.Windows = append(snap.Windows, ws)
			}
		}
		manifest.Sessions = append(manifest.Sessions, snap)
	}

	if err := TuneOomPanes(sessions); err != nil {
		s.log.WithError(err).Debug("oom tuning failed")
	}

	fp := manifest.fingerprint()
	if fp != "" && fp == s.lastFP {
		return nil
	}
	s.gen++
	manifest.Generation = s.gen
	manifest.UpdatedAt = time.Now()
	if err := manifest.Save(); err != nil {
		return err
	}
	s.lastFP = fp
	return nil
}
