package toolevents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// TmuxClient is the subset of tmux.Client used by SilenceMonitor
type TmuxClient interface {
	CapturePaneContent(paneID string) (string, error)
}

// silenceThreshold is how long a pane must be quiet before we check for prompts
const silenceThreshold = 10 * time.Second

// maxSilenceChecks is the number of times we'll capture-pane after a pane
// goes silent. After this many checks with no prompt detected, we stop
// until output resumes.
const maxSilenceChecks = 2

// staleWaitingGrace is how long a retained waiting event must have existed
// before the reaper will clear it on a no-prompt capture. Guards against
// clearing a freshly-set waiting before its dialog has rendered on screen.
const staleWaitingGrace = 8 * time.Second

// monitoredPane tracks state for a single monitored pane
type monitoredPane struct {
	tool          Tool
	session       string    // tmux session name (captured at sync time)
	window        int       // tmux window index (captured at sync time)
	lastOutput    time.Time // last time we saw output for this pane
	prompted      bool      // true if we already recorded a waiting event
	silenceChecks int       // how many times we've checked since going silent
}

// SilenceMonitor watches panes running non-Claude agents. It tracks output
// activity via RecordOutput (called from the %output handler) and periodically
// checks panes that have gone quiet for input prompts via capture-pane.
type SilenceMonitor struct {
	tracker  *Tracker
	detector *Detector
	client   TmuxClient
	log      *logrus.Entry
	hostID   string // local host fingerprint (for multi-host)
	hostName string // local host display name

	mu        sync.Mutex
	monitored map[string]*monitoredPane // paneID → state
}

// NewSilenceMonitor creates a new SilenceMonitor.
func NewSilenceMonitor(tracker *Tracker, detector *Detector, client TmuxClient) *SilenceMonitor {
	return &SilenceMonitor{
		tracker:   tracker,
		detector:  detector,
		client:    client,
		log:       logrus.WithField("component", "silence-monitor"),
		monitored: make(map[string]*monitoredPane),
	}
}

// SetHost sets the local host identity for multi-host event stamping.
func (sm *SilenceMonitor) SetHost(id, name string) {
	sm.hostID = id
	sm.hostName = name
}

// RecordOutput notes that a pane produced output, resetting its silence timer.
// Called from the control mode %output handler.
func (sm *SilenceMonitor) RecordOutput(paneID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if mp, ok := sm.monitored[paneID]; ok {
		mp.lastOutput = time.Now()
		if mp.prompted || mp.silenceChecks > 0 {
			sm.log.WithField("pane", paneID).Trace("output resumed, resetting silence state")
			mp.prompted = false
			mp.silenceChecks = 0
		}
	}
}

// Run periodically syncs the set of monitored panes with the detector's
// detected panes, then checks quiet panes for input prompts.
// Blocks until ctx is cancelled.
func (sm *SilenceMonitor) Run(ctx context.Context) {
	sm.log.Info("starting silence monitor")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sm.log.Info("stopping silence monitor")
			return
		case <-ticker.C:
			sm.sync()
			sm.checkSilentPanes()
		}
	}
}

// sync updates the set of monitored panes based on current detections.
func (sm *SilenceMonitor) sync() {
	detected := sm.detector.DetectedPanes()

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.log.WithFields(logrus.Fields{
		"detected_count":  len(detected),
		"monitored_count": len(sm.monitored),
		"detected":        detected,
	}).Trace("syncing monitored panes")

	// Monitor every detected agent pane. Native-hook agents (Claude/Pi/
	// OpenCode) are not silence-promoted to waiting (they self-report), but we
	// still capture them so the reaper can clear a stale native waiting once
	// its input dialog is dismissed — Claude emits no hook on cancel.
	for paneID, tool := range detected {
		if _, already := sm.monitored[paneID]; already {
			continue
		}

		info := sm.detector.PaneInfo(paneID)

		sm.log.WithFields(logrus.Fields{
			"pane":    paneID,
			"tool":    tool,
			"session": info.Session,
			"window":  info.Window,
		}).Debug("now monitoring pane for silence")

		sm.monitored[paneID] = &monitoredPane{
			tool:       tool,
			session:    info.Session,
			window:     info.Window,
			lastOutput: time.Now(), // assume active when first detected
		}
	}

	// Remove departed panes
	for paneID := range sm.monitored {
		if _, stillPresent := detected[paneID]; !stillPresent {
			sm.log.WithField("pane", paneID).Debug("stopping silence monitoring for pane")
			delete(sm.monitored, paneID)
		}
	}
}

// checkSilentPanes captures content from panes that have been quiet longer
// than silenceThreshold and checks for input prompts.
func (sm *SilenceMonitor) checkSilentPanes() {
	now := time.Now()

	// Collect panes to check under lock
	sm.mu.Lock()
	if len(sm.monitored) == 0 {
		sm.mu.Unlock()
		return
	}
	type checkTarget struct {
		paneID  string
		tool    Tool
		session string
		window  int
	}
	var targets []checkTarget
	for paneID, mp := range sm.monitored {
		if mp.prompted || mp.silenceChecks >= maxSilenceChecks {
			continue // already handled or exhausted checks
		}
		if now.Sub(mp.lastOutput) >= silenceThreshold {
			mp.silenceChecks++
			targets = append(targets, checkTarget{paneID: paneID, tool: mp.tool, session: mp.session, window: mp.window})
		}
	}
	sm.mu.Unlock()

	for _, t := range targets {
		sm.log.WithFields(logrus.Fields{
			"pane": t.paneID,
			"tool": t.tool,
		}).Trace("pane has been silent, checking for prompt")

		content, err := sm.client.CapturePaneContent(t.paneID)
		if err != nil {
			sm.log.WithError(err).WithField("pane", t.paneID).Warn("failed to capture pane content")
			continue
		}

		sm.log.WithFields(logrus.Fields{
			"pane":    t.paneID,
			"content": fmt.Sprintf("%.200s", content),
		}).Trace("capture-pane content")

		result := DetectPrompt(content)

		if result.IsPrompt {
			// Stop re-capturing this pane for the rest of the silence period.
			sm.mu.Lock()
			if mp, ok := sm.monitored[t.paneID]; ok {
				mp.prompted = true
			}
			sm.mu.Unlock()

			// Native-hook agents report their own waiting; capturing their
			// dialog only confirms it is still on screen so the reaper below
			// does not clear it prematurely. Do not synthesize a waiting event.
			if nativeWaitingTools[t.tool] {
				sm.log.WithFields(logrus.Fields{
					"pane": t.paneID,
					"tool": t.tool,
				}).Trace("native prompt still present; not synthesizing waiting")
				continue
			}

			sm.log.WithFields(logrus.Fields{
				"pane":    t.paneID,
				"tool":    t.tool,
				"message": result.Message,
			}).Debug("prompt detected")

			sm.tracker.Record(&Event{
				Tool:         t.tool,
				Status:       StatusWaiting,
				Host:         sm.hostID,
				HostName:     sm.hostName,
				Session:      t.session,
				Window:       t.window,
				Pane:         t.paneID,
				Message:      result.Message,
				AutoDetected: true,
			})
		} else {
			// No input dialog on screen. A native-hook agent that emits no
			// cancel hook (Claude) can be left with a stale retained waiting
			// after the user dismisses its dialog. Clear it so the badge
			// returns to idle. Scoped to Claude to avoid racing agents that
			// clear their own waiting via active/completed on resume.
			if t.tool == ToolClaude {
				if w := sm.tracker.RetainedWaitingForPane(t.paneID); w != nil && time.Since(w.Timestamp) > staleWaitingGrace {
					sm.log.WithFields(logrus.Fields{
						"pane": t.paneID,
						"tool": t.tool,
					}).Debug("clearing stale claude waiting (input dialog dismissed)")
					sm.tracker.Record(&Event{
						Tool:         t.tool,
						Status:       StatusCompleted,
						Host:         sm.hostID,
						HostName:     sm.hostName,
						Session:      t.session,
						Window:       t.window,
						Pane:         t.paneID,
						Message:      "Returned to prompt",
						AutoDetected: true,
					})
				}
			}

			tail := content
			if len(tail) > 300 {
				tail = tail[len(tail)-300:]
			}
			sm.log.WithFields(logrus.Fields{
				"pane":         t.paneID,
				"tool":         t.tool,
				"content_tail": tail,
			}).Trace("no prompt detected (silence miss)")
		}
	}
}

