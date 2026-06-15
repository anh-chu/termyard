package scheduler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// Job is one scheduled command.
type Job struct {
	ID                string    `json:"id"`
	Name              string    `json:"name,omitempty"`
	CronSpec          string    `json:"cron_spec"`
	Command           string    `json:"command,omitempty"`
	Path              string    `json:"path,omitempty"`
	AgentType         string    `json:"agent_type,omitempty"`
	Host              string    `json:"host,omitempty"`
	SessionNamePrefix string    `json:"session_name_prefix,omitempty"`
	WorktreeBranch    string    `json:"worktree_branch,omitempty"`
	MaxConcurrency    int       `json:"max_concurrency,omitempty"`
	Enabled           bool      `json:"enabled"`
	LastRun           time.Time `json:"last_run,omitempty"`
	NextRun           time.Time `json:"next_run,omitempty"`
	RunCount          int       `json:"run_count,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

// Store persists schedules to ~/.config/guppi/schedules.json.
type Store struct {
	mu   sync.RWMutex
	path string
	jobs map[string]Job
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "guppi"), nil
}

func NewStore() (*Store, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		path: filepath.Join(dir, "schedules.json"),
		jobs: map[string]Job{},
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var jobs []Job
	if err := json.Unmarshal(raw, &jobs); err != nil {
		return err
	}
	s.jobs = map[string]Job{}
	for _, job := range jobs {
		if job.ID == "" {
			continue
		}
		s.jobs[job.ID] = job
	}
	return nil
}

func (s *Store) saveLocked() error {
	jobs := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	raw, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}

func (s *Store) validate(job Job) error {
	if job.CronSpec == "" {
		return fmt.Errorf("cron spec is required")
	}
	if _, err := cron.ParseStandard(job.CronSpec); err != nil {
		return fmt.Errorf("invalid cron spec: %w", err)
	}
	return nil
}

func (s *Store) Add(job Job) (Job, error) {
	if err := s.validate(job); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.ID == "" {
		job.ID = newID()
	}
	if _, exists := s.jobs[job.ID]; exists {
		return Job{}, fmt.Errorf("job already exists")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}
	if job.Enabled {
		job.NextRun = nextRun(job.CronSpec, time.Now())
	}
	s.jobs[job.ID] = job
	return job, s.saveLocked()
}

func (s *Store) Update(job Job) (Job, error) {
	if job.ID == "" {
		return Job{}, fmt.Errorf("job id is required")
	}
	if err := s.validate(job); err != nil {
		return Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, exists := s.jobs[job.ID]
	if !exists {
		return Job{}, fmt.Errorf("job not found")
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = cur.CreatedAt
	}
	if job.LastRun.IsZero() {
		job.LastRun = cur.LastRun
	}
	if job.SessionNamePrefix == "" {
		job.SessionNamePrefix = cur.SessionNamePrefix
	}
	if job.RunCount == 0 && cur.RunCount != 0 {
		job.RunCount = cur.RunCount
	}
	s.jobs[job.ID] = job
	return job, s.saveLocked()
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return fmt.Errorf("job not found")
	}
	delete(s.jobs, id)
	return s.saveLocked()
}

func (s *Store) List() []Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	jobs := make([]Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs
}

func (s *Store) Get(id string) (Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	return job, ok
}

func (s *Store) MarkRan(id string, lastRun, nextRun time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("job not found")
	}
	job.LastRun = lastRun
	job.NextRun = nextRun
	job.RunCount++
	s.jobs[id] = job
	return job, s.saveLocked()
}

func (s *Store) disable(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job not found")
	}
	job.Enabled = false
	job.NextRun = time.Time{}
	s.jobs[id] = job
	return s.saveLocked()
}

func (s *Store) reconcileNextRuns(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id, job := range s.jobs {
		if !job.Enabled {
			continue
		}
		next, err := nextRunFor(job.CronSpec, now)
		if err != nil {
			continue
		}
		if !job.NextRun.Equal(next) {
			job.NextRun = next
			s.jobs[id] = job
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked()
}

func nextRun(spec string, now time.Time) time.Time {
	next, err := nextRunFor(spec, now)
	if err != nil {
		return time.Time{}
	}
	return next
}

func nextRunFor(spec string, now time.Time) (time.Time, error) {
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(now), nil
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("%x", b[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
