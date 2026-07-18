package recovery

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/anh-chu/termyard/pkg/model"
)

type serverProbe interface {
	ServerAlive() bool
	ListSessions() ([]*model.Session, error)
}

// HealthPoller watches tmux server liveness and triggers recovery.
//
// Recovery fires only on tmux server death (a crash). It deliberately does NOT
// rebuild individual sessions that go missing while the server is alive: those
// are intentional kills, and resurrecting them is the auto-recovery respawn bug.
type HealthPoller struct {
	client   serverProbe
	interval time.Duration
	onGone   func()
	wasAlive bool
	hintCh   chan struct{}
	log      *logrus.Entry
}

func NewHealthPoller(client serverProbe, interval time.Duration, onGone func()) *HealthPoller {
	return &HealthPoller{
		client:   client,
		interval: interval,
		onGone:   onGone,
		hintCh:   make(chan struct{}, 1),
		log:      logrus.WithField("component", "recovery-health"),
	}
}

func (h *HealthPoller) Run(ctx context.Context) {
	// Daemon sessions survive server crashes. No tmux health polling needed.
	if h == nil {
		return
	}
	<-ctx.Done()
}

func (h *HealthPoller) Hint() {
	if h == nil {
		return
	}
	select {
	case h.hintCh <- struct{}{}:
	default:
	}
}

func (h *HealthPoller) probe() {
	if h == nil || h.client == nil {
		return
	}
	alive := h.client.ServerAlive()
	if !alive {
		if h.wasAlive {
			h.wasAlive = false
			if manifestHasSessions() {
				h.maybeTrigger()
			}
		}
		return
	}

	h.wasAlive = true
}

func (h *HealthPoller) maybeTrigger() {
	if h == nil || h.onGone == nil {
		return
	}
	h.onGone()
}

func manifestHasSessions() bool {
	m, err := Load()
	return err == nil && m != nil && len(m.Sessions) > 0
}
