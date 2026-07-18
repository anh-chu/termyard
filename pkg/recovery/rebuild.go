package recovery

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/sessionattrs"
	"github.com/anh-chu/termyard/pkg/state"
	"github.com/anh-chu/termyard/pkg/model"
)

type rebuildClient interface {
	HasSession(name string) bool
	NewSession(name, projectPath, command string) error
	NewWindow(session, name, projectPath, command string) error
	SplitWindow(target, projectPath, command string) error
	SelectLayout(target, layout string) error
	SelectWindow(session, index string) error
	SelectPane(target string) error
	SetScheduleID(name, scheduleID string) error
}

// Rebuilder recreates sessions from manifest after tmux loss.
type Rebuilder struct {
	client   rebuildClient
	stateMgr *state.Manager
	attrs    *sessionattrs.Store
	log      *logrus.Entry
}

func NewRebuilder(client rebuildClient, sm *state.Manager, attrs *sessionattrs.Store) *Rebuilder {
	return &Rebuilder{
		client:   client,
		stateMgr: sm,
		attrs:    attrs,
		log:      logrus.WithField("component", "recovery-rebuild"),
	}
}

func (r *Rebuilder) Rebuild(ctx context.Context) error {
	// Daemon sessions survive server crashes on their own.
	// No tmux rebuild needed.
	_ = ctx
	return nil
}

func (r *Rebuilder) rebuildSession(session SessionSnapshot) error {
	if len(session.Windows) == 0 {
		return nil
	}
	windows := append([]WindowSnapshot(nil), session.Windows...)
	sort.Slice(windows, func(i, j int) bool { return windows[i].Index < windows[j].Index })

	first := windows[0]
	if len(first.Panes) == 0 {
		return nil
	}
	firstPane := first.Panes[0]
	if err := r.client.NewSession(session.Name, firstPane.CWD, paneStartCommand(session, firstPane)); err != nil {
		return err
	}
	if r.stateMgr != nil {
		r.stateMgr.SetSessionAgentType(session.Name, session.AgentType)
	}
	// Restore schedule ownership so the rebuilt session rejoins its schedule
	// group and stays subject to the concurrency cap. The tmux user-option is the
	// durable source of truth; the attr write is the one-release fallback.
	if session.ScheduleID != "" {
		if err := r.client.SetScheduleID(session.Name, session.ScheduleID); err != nil {
			r.log.WithError(err).WithField("session", session.Name).Warn("failed to restore schedule id option")
		}
		if r.attrs != nil {
			if _, err := r.attrs.SetScheduleID(session.Name, session.ScheduleID); err != nil {
				r.log.WithError(err).WithField("session", session.Name).Warn("failed to restore schedule id")
			}
		}
	}

	for i, win := range windows {
		if len(win.Panes) == 0 {
			continue
		}
		winTarget := windowTarget(session.Name, win.Index)
		if i > 0 {
			if err := r.client.NewWindow(winTarget, win.Name, win.Panes[0].CWD, paneStartCommand(session, win.Panes[0])); err != nil {
				return err
			}
		}

		for _, pane := range win.Panes[1:] {
			if err := r.client.SplitWindow(winTarget, pane.CWD, paneStartCommand(session, pane)); err != nil {
				return err
			}
		}
		if win.Layout != "" {
			if err := r.client.SelectLayout(winTarget, win.Layout); err != nil {
				r.log.WithError(err).WithFields(logrus.Fields{"session": session.Name, "layout": win.Layout}).Debug("layout restore failed")
			}
		}
		if win.Active {
			if pane := activePane(win); pane != nil {
				if err := r.client.SelectPane(paneTarget(session.Name, win.Index, pane.Index)); err != nil {
					r.log.WithError(err).WithFields(logrus.Fields{"session": session.Name, "window": win.Index, "pane": pane.Index}).Debug("pane restore failed")
				}
			}
		}
	}

	activeIndex := "0"
	for _, win := range windows {
		if win.Active {
			activeIndex = strconv.Itoa(win.Index)
			break
		}
	}
	_ = r.client.SelectWindow(session.Name, activeIndex)
	return nil
}

func activePane(win WindowSnapshot) *PaneSnapshot {
	for i := range win.Panes {
		if win.Panes[i].Active {
			return &win.Panes[i]
		}
	}
	if len(win.Panes) > 0 {
		return &win.Panes[0]
	}
	return nil
}

func paneStartCommand(session SessionSnapshot, pane PaneSnapshot) string {
	if pane.StartCommand != "" {
		return pane.StartCommand
	}
	return buildStartCommand(session.AgentType, session.AgentSessionID, pane.CWD, pane.CurrentCommand)
}

func windowTarget(sessionName string, index int) string {
	return fmt.Sprintf("%s:%d", sessionName, index)
}

func paneTarget(sessionName string, windowIndex, paneIndex int) string {
	return fmt.Sprintf("%s:%d.%d", sessionName, windowIndex, paneIndex)
}

func buildStartCommand(agentType, agentSessionID, cwd, currentCommand string) string {
	switch model.NormalizeAgentType(agentType) {
	case "pi":
		if agentSessionID != "" {
			return "pi --resume " + shellQuote(agentSessionID)
		}
		return "pi"
	case "claude":
		if agentSessionID != "" {
			return "claude --resume " + shellQuote(agentSessionID)
		}
		return "claude"
	case "codex":
		if agentSessionID != "" {
			return "codex resume " + shellQuote(agentSessionID)
		}
		return "codex"
	case "opencode":
		if agentSessionID != "" {
			return "opencode --session " + shellQuote(agentSessionID)
		}
		return "opencode"
	default:
		_ = cwd
		_ = currentCommand
		return ""
	}
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}
