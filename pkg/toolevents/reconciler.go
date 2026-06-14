package toolevents

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

// PaneState describes the current state of a tmux pane for reconciliation
type PaneState struct {
	Exists         bool
	CurrentCommand string
	PID            int
}

// PaneLookupFunc returns the state of a pane given its ID.
// If the pane does not exist, it should return PaneState{Exists: false}.
type PaneLookupFunc func(paneID string) PaneState

// PaneInfo contains identifying information for a tmux pane, used by the
// agent detector to scan panes for running agents.
type PaneInfo struct {
	PaneID  string
	Session string // session name
	Window  int    // window index
	PID     int    // pane process PID
}

// PaneListFunc returns all currently known panes.
type PaneListFunc func() []PaneInfo

// Reconciler periodically checks tracked tool events against actual tmux pane
// state, clearing events whose agent process is no longer running.
type Reconciler struct {
	tracker  *Tracker
	lookup   PaneLookupFunc
	interval time.Duration
	log      *logrus.Entry
	hostID   string // local host fingerprint (for multi-host)
	hostName string // local host display name
}

// SetHost sets the local host identity for multi-host event stamping.
// Without this the synthetic completed events carry no host, so the frontend
// keys them as bare "session" instead of "host/session" and fails to clear
// session-level active-turn tracking, leaving the badge stuck on "working".
func (r *Reconciler) SetHost(id, name string) {
	r.hostID = id
	r.hostName = name
}

// NewReconciler creates a reconciler that checks tracked events against pane state.
func NewReconciler(tracker *Tracker, lookup PaneLookupFunc, interval time.Duration) *Reconciler {
	return &Reconciler{
		tracker:  tracker,
		lookup:   lookup,
		interval: interval,
		log:      logrus.WithField("component", "tool-reconciler"),
	}
}

// Run starts the reconciliation loop. It blocks until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	r.log.WithField("interval", r.interval).Info("starting tool event reconciler")

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info("stopping tool event reconciler")
			return
		case <-ticker.C:
			r.reconcile()
		}
	}
}

// reconcile checks all tracked events and clears any whose pane no longer
// has an agent running.
func (r *Reconciler) reconcile() {
	// Collect all events to check: stored waiting/error/stuck events plus
	// hook-based active panes. Active events clear t.events, so without the
	// second source the reconciler would miss agents that exit after sending
	// only an active hook (e.g. on force-quit).
	events := r.tracker.GetAll()
	activePaneEvents := r.tracker.GetActivePaneEvents()

	all := append(events, activePaneEvents...)
	if len(all) == 0 {
		return
	}

	// Track processed panes to avoid double-clearing (t.events and activePanes
	// are mutually exclusive by design, but guard defensively).
	checked := make(map[string]bool, len(all))

	for _, evt := range all {
		if evt.Pane == "" {
			continue
		}
		if checked[evt.Pane] {
			continue
		}
		checked[evt.Pane] = true

		ps := r.lookup(evt.Pane)

		// Simple, robust rule: an event is only valid while the agent process
		// is still running in the pane. The moment the pane's process tree no
		// longer contains a known agent (it exited back to a shell, or the pane
		// is gone), clear the event. This replaces the old shell-name whitelist,
		// which missed shells not in the map and misread wrapper commands like
		// "node" as "still an agent".
		shouldClear := false
		if !ps.Exists {
			r.log.WithField("pane", evt.Pane).Debug("pane no longer exists, clearing event")
			shouldClear = true
		} else if _, found := DetectAgentInProcessTree(ps.PID); !found {
			r.log.WithFields(logrus.Fields{
				"pane":    evt.Pane,
				"command": ps.CurrentCommand,
				"pid":     ps.PID,
			}).Debug("no agent in pane process tree, clearing event")
			shouldClear = true
		}

		if shouldClear {
			// Record a synthetic completed event to clear tracking and notify subscribers
			r.tracker.Record(&Event{
				Tool:     evt.Tool,
				Status:   StatusCompleted,
				Host:     r.hostID,
				HostName: r.hostName,
				Session:  evt.Session,
				Window:   evt.Window,
				Pane:     evt.Pane,
				Message:  "auto-cleared: agent no longer running",
			})
		}
	}
}
