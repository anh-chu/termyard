package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"

	"github.com/ekristen/guppi/pkg/peer"
	"github.com/ekristen/guppi/pkg/state"
	"github.com/ekristen/guppi/pkg/tmux"
)

// CreateSessionReq reuses the session-spawn contract across HTTP and cron.
type CreateSessionReq struct {
	Name           string
	Host           string
	Path           string
	Command        string
	AgentType      string
	WorktreeBranch string
	ScheduleID     string
}

// CreateSessionFunc spawns one session.
type CreateSessionFunc func(CreateSessionReq) error

type PeerLookup interface {
	IsLocal(hostID string) bool
	GetPeerConnection(id string) *peer.PeerConnection
}

// Runner fires due jobs on a 1s tick.
type Runner struct {
	store    *Store
	client   *tmux.Client
	stateMgr *state.Manager
	peerMgr  PeerLookup
	createFn CreateSessionFunc
	log      *logrus.Entry
	nowFn    func() time.Time
}

func NewRunner(store *Store, client *tmux.Client, stateMgr *state.Manager, peerMgr PeerLookup, createFn CreateSessionFunc, log *logrus.Entry) *Runner {
	if log == nil {
		log = logrus.NewEntry(logrus.StandardLogger())
	}
	return &Runner{
		store:    store,
		client:   client,
		stateMgr: stateMgr,
		peerMgr:  peerMgr,
		createFn: createFn,
		log:      log,
		nowFn:    time.Now,
	}
}

func (r *Runner) Run(ctx context.Context) {
	if r == nil || r.store == nil || r.createFn == nil {
		return
	}
	nowFn := r.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}
	if err := r.store.reconcileNextRuns(nowFn()); err != nil {
		r.log.WithError(err).Warn("scheduler startup reconcile failed")
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(nowFn())
		}
	}
}

func (r *Runner) runOnce(now time.Time) {
	for _, job := range r.store.List() {
		if !job.Enabled || job.CronSpec == "" {
			continue
		}
		if job.NextRun.After(now) {
			continue
		}
		schedule, err := cron.ParseStandard(job.CronSpec)
		if err != nil {
			r.log.WithError(err).WithField("job_id", job.ID).Warn("scheduler job disabled: invalid cron")
			if disErr := r.store.disable(job.ID); disErr != nil {
				r.log.WithError(disErr).WithField("job_id", job.ID).Warn("scheduler disable update failed")
			}
			continue
		}
		next := schedule.Next(now)
		if next.IsZero() {
			next = now.Add(time.Minute)
		}

		if job.Host != "" && r.peerMgr != nil && !r.peerMgr.IsLocal(job.Host) {
			if r.peerMgr.GetPeerConnection(job.Host) == nil {
				r.log.WithField("job_id", job.ID).WithField("host", job.Host).Warn("scheduler peer offline, skipping fire")
				job.NextRun = next
				if _, updErr := r.store.Update(job); updErr != nil {
					r.log.WithError(updErr).WithField("job_id", job.ID).Warn("scheduler next-run update failed")
				}
				continue
			}
		}

		name := job.SessionNamePrefix
		if name == "" {
			name = job.Name
		}
		if name == "" {
			name = "schedule"
		}
		req := CreateSessionReq{
			Name:           fmt.Sprintf("%s-%d", name, now.Unix()),
			Host:           job.Host,
			Path:           job.Path,
			Command:        job.Command,
			AgentType:      job.AgentType,
			WorktreeBranch: job.WorktreeBranch,
			ScheduleID:     job.ID,
		}
		if err := r.createFn(req); err != nil {
			r.log.WithError(err).WithField("job_id", job.ID).Warn("scheduler fire failed")
			job.NextRun = next
			if _, updErr := r.store.Update(job); updErr != nil {
				r.log.WithError(updErr).WithField("job_id", job.ID).Warn("scheduler next-run update failed")
			}
			continue
		}
		if _, err := r.store.MarkRan(job.ID, now, next); err != nil {
			r.log.WithError(err).WithField("job_id", job.ID).Warn("scheduler mark-ran failed")
		}
	}
}
