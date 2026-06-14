package recovery

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/ekristen/guppi/pkg/tmux"
)

type serverProbe interface {
	ServerAlive() bool
	ListSessions() ([]*tmux.Session, error)
}

// HealthPoller watches tmux server liveness and triggers recovery.
type HealthPoller struct {
	client           serverProbe
	interval         time.Duration
	onGone           func()
	wasAlive         bool
	missingTriggered bool
	hintCh           chan struct{}
	log              *logrus.Entry
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
	if h == nil {
		return
	}
	h.wasAlive = h.client != nil && h.client.ServerAlive()
	if !h.wasAlive && manifestHasSessions() {
		h.maybeTrigger()
	}
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.probe()
		case <-h.hintCh:
			h.probe()
		}
	}
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
			h.missingTriggered = false
			if manifestHasSessions() {
				h.maybeTrigger()
			}
		}
		return
	}

	h.wasAlive = true
	manifest, err := Load()
	if err != nil || manifest == nil || len(manifest.Sessions) == 0 {
		h.missingTriggered = false
		return
	}

	sessions, err := h.client.ListSessions()
	if err != nil {
		return
	}
	if sessionsMissing(manifest.Sessions, sessions) {
		if !h.missingTriggered {
			h.missingTriggered = true
			h.maybeTrigger()
		}
		return
	}

	h.missingTriggered = false
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

func sessionsMissing(manifest []SessionSnapshot, current []*tmux.Session) bool {
	if len(manifest) == 0 {
		return false
	}
	currentByName := make(map[string]struct{}, len(current))
	for _, session := range current {
		if session == nil || session.Name == "" {
			continue
		}
		currentByName[session.Name] = struct{}{}
	}
	for _, session := range manifest {
		if session.Name == "" {
			continue
		}
		if _, ok := currentByName[session.Name]; !ok {
			return true
		}
	}
	return false
}
