package toolevents

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// TestStuckFlaggedAfterTimeout verifies that a local pane reporting "active"
// with no further progress is promoted to "stuck" by checkStuck.
func TestStuckFlaggedAfterTimeout(t *testing.T) {
	tr := NewTracker()
	ch := tr.Subscribe()
	defer tr.Unsubscribe(ch)

	tr.Record(&Event{Tool: ToolClaude, Status: StatusActive, Session: "s1", Window: 0, Pane: "%1"})
	drain(ch) // consume active broadcast

	// Backdate progress so it looks stale.
	tr.mu.Lock()
	tr.activePanes["%1"].lastProgress = time.Now().Add(-10 * time.Minute)
	tr.mu.Unlock()

	tr.checkStuck(5*time.Minute, nil, logrus.WithField("test", "stuck"))

	evt := waitFor(t, ch, StatusStuck)
	if evt.Tool != ToolClaude || evt.Pane != "%1" {
		t.Fatalf("unexpected stuck event: %+v", evt)
	}

	// Tracked as a real (non-active) event so it surfaces in GetAll.
	found := false
	for _, e := range tr.GetAll() {
		if e.Status == StatusStuck && e.Pane == "%1" {
			found = true
		}
	}
	if !found {
		t.Fatal("stuck event not present in GetAll")
	}
}

// TestStuckNotFlaggedWhenAtPrompt verifies a quiet pane sitting at an input
// prompt is treated as waiting (silence monitor's job), not stuck.
func TestStuckNotFlaggedWhenAtPrompt(t *testing.T) {
	tr := NewTracker()
	tr.Record(&Event{Tool: ToolClaude, Status: StatusActive, Session: "s1", Window: 0, Pane: "%1"})

	tr.mu.Lock()
	tr.activePanes["%1"].lastProgress = time.Now().Add(-10 * time.Minute)
	tr.mu.Unlock()

	atPrompt := func(string) (bool, bool) { return true, true }
	tr.checkStuck(5*time.Minute, atPrompt, logrus.WithField("test", "stuck"))

	tr.mu.Lock()
	flagged := tr.activePanes["%1"].flagged
	tr.mu.Unlock()
	if flagged {
		t.Fatal("pane at prompt should not be flagged as stuck")
	}
}

// TestRecordProgressClearsStuck verifies output after a stuck flag emits an
// active event and unflags the pane.
func TestRecordProgressClearsStuck(t *testing.T) {
	tr := NewTracker()
	ch := tr.Subscribe()
	defer tr.Unsubscribe(ch)

	tr.Record(&Event{Tool: ToolClaude, Status: StatusActive, Session: "s1", Window: 0, Pane: "%1"})
	drain(ch)

	tr.mu.Lock()
	tr.activePanes["%1"].lastProgress = time.Now().Add(-10 * time.Minute)
	tr.mu.Unlock()
	tr.checkStuck(5*time.Minute, nil, logrus.WithField("test", "stuck"))
	waitFor(t, ch, StatusStuck)

	tr.RecordProgress("%1")

	evt := waitFor(t, ch, StatusActive)
	if evt.Pane != "%1" {
		t.Fatalf("expected active clear for %%1, got %+v", evt)
	}
	tr.mu.Lock()
	flagged := tr.activePanes["%1"].flagged
	tr.mu.Unlock()
	if flagged {
		t.Fatal("RecordProgress should clear the stuck flag")
	}
}

// TestWaitingClearsActivePane verifies an explicit waiting event removes the
// pane from stuck tracking (agent no longer claims to be working).
func TestWaitingClearsActivePane(t *testing.T) {
	tr := NewTracker()
	tr.Record(&Event{Tool: ToolClaude, Status: StatusActive, Session: "s1", Window: 0, Pane: "%1"})
	tr.Record(&Event{Tool: ToolClaude, Status: StatusWaiting, Session: "s1", Window: 0, Pane: "%1"})

	tr.mu.Lock()
	_, present := tr.activePanes["%1"]
	tr.mu.Unlock()
	if present {
		t.Fatal("waiting event should remove pane from activePanes")
	}
}

// TestRemotePaneNotTracked verifies peer (non-local) panes are excluded.
func TestRemotePaneNotTracked(t *testing.T) {
	tr := NewTracker()
	tr.Record(&Event{Tool: ToolClaude, Status: StatusActive, Host: "peer1", Session: "s1", Window: 0, Pane: "%1"})

	tr.mu.Lock()
	n := len(tr.activePanes)
	tr.mu.Unlock()
	if n != 0 {
		t.Fatalf("remote pane should not be tracked, got %d entries", n)
	}
}

func drain(ch chan *Event) {
	select {
	case <-ch:
	case <-time.After(time.Second):
	}
}

func waitFor(t *testing.T, ch chan *Event, status Status) *Event {
	t.Helper()
	for {
		select {
		case evt := <-ch:
			if evt.Status == status {
				return evt
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s event", status)
			return nil
		}
	}
}
