// Package scheduler drives time-based backup runs using cron expressions or
// fixed intervals. It reads its schedule from the config store, so a Reload
// after any job edit is all that is needed to pick up changes.
package scheduler

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/qzrsa/qzrs-webdav-backup/internal/config"
	"github.com/qzrsa/qzrs-webdav-backup/internal/engine"
)

// Scheduler owns the cron engine and the mapping from job id to cron entry.
type Scheduler struct {
	runner *engine.Runner
	store  *config.Store
	cron   *cron.Cron
	logf   func(string, ...any)

	mu      sync.Mutex
	entries map[string]cron.EntryID
	invalid map[string]string // jobID -> reason the schedule was rejected
}

// New builds a scheduler. The cron engine is not started until Start is called.
func New(runner *engine.Runner, store *config.Store, logf func(string, ...any)) *Scheduler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Scheduler{
		runner:  runner,
		store:   store,
		logf:    logf,
		entries: map[string]cron.EntryID{},
		invalid: map[string]string{},
		cron: cron.New(
			cron.WithLocation(time.Local),
			// Standard 5-field expressions: minute hour dom month dow.
			cron.WithParser(cron.NewParser(
				cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow|cron.Descriptor,
			)),
			cron.WithChain(
				cron.Recover(cronLogger{logf}),
				// Skip a tick if the previous execution of the same entry is
				// still running rather than stacking up.
				cron.SkipIfStillRunning(cronLogger{logf}),
			),
		),
	}
}

// Start begins scheduling and applies the current configuration.
func (s *Scheduler) Start() error {
	if err := s.Reload(); err != nil {
		return err
	}
	s.cron.Start()
	s.logf("scheduler started with %d active job(s)", len(s.entries))
	return nil
}

// Stop halts scheduling and waits for running jobs to return.
func (s *Scheduler) Stop() {
	if s.cron == nil {
		return
	}
	ctx := s.cron.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		s.logf("scheduler stop timed out; some jobs may still be finishing")
	}
}

// Reload rebuilds every cron entry from the current configuration. It is safe
// to call while the scheduler is running.
func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range s.entries {
		s.cron.Remove(id)
	}
	s.entries = map[string]cron.EntryID{}
	s.invalid = map[string]string{}

	cfg := s.store.Snapshot()
	for i := range cfg.Jobs {
		job := &cfg.Jobs[i]
		if !job.Enabled {
			continue
		}
		spec, err := Spec(job)
		if err != nil {
			s.invalid[job.ID] = err.Error()
			s.logf("job %q disabled: %v", job.Name, err)
			continue
		}
		if spec == "" {
			continue // manual-only job
		}
		jobID, jobName := job.ID, job.Name
		entryID, err := s.cron.AddFunc(spec, func() { s.fire(jobID, jobName) })
		if err != nil {
			s.invalid[job.ID] = err.Error()
			s.logf("job %q has an unusable schedule %q: %v", jobName, spec, err)
			continue
		}
		s.entries[jobID] = entryID
	}
	return nil
}

// Spec converts a job's schedule into a cron specification. It returns an empty
// string for manual-only jobs.
func Spec(job *config.Job) (string, error) {
	switch job.Schedule.Mode {
	case "", config.ScheduleManual:
		return "", nil
	case config.ScheduleCron:
		expr := job.Schedule.Cron
		if expr == "" {
			return "", fmt.Errorf("cron mode selected but the expression is empty")
		}
		if _, err := cron.ParseStandard(expr); err != nil {
			return "", fmt.Errorf("invalid cron expression %q: %w", expr, err)
		}
		return expr, nil
	case config.ScheduleInterval:
		d, err := time.ParseDuration(job.Schedule.Interval)
		if err != nil {
			return "", fmt.Errorf("invalid interval %q: %w", job.Schedule.Interval, err)
		}
		if d < time.Minute {
			return "", fmt.Errorf("interval %s is too short; the minimum is 1m", job.Schedule.Interval)
		}
		return "@every " + d.String(), nil
	default:
		return "", fmt.Errorf("unknown schedule mode %q", job.Schedule.Mode)
	}
}

func (s *Scheduler) fire(jobID, jobName string) {
	if s.runner.Running(jobID) {
		s.logf("scheduled run of %q skipped: the previous run is still in progress", jobName)
		return
	}
	s.logf("scheduled run starting: %s", jobName)
	end := time.Now()
	_ = end

	// A background context: the run outlives the scheduler tick and is bounded
	// by the WebDAV client's own timeouts plus manual cancellation.
	run, err := s.runner.Backup(context.Background(), jobID, engine.TriggerSchedule)
	if err != nil {
		s.logf("scheduled run of %q could not start: %v", jobName, err)
		return
	}
	s.logf("scheduled run of %q finished: %s (%s)", jobName, run.Status, run.Message)
}

// ScheduleInfo describes the runtime schedule state of one job.
type ScheduleInfo struct {
	JobID   string     `json:"job_id"`
	Spec    string     `json:"spec,omitempty"`
	Active  bool       `json:"active"`
	NextRun *time.Time `json:"next_run,omitempty"`
	LastRun *time.Time `json:"last_run,omitempty"`
	Error   string     `json:"error,omitempty"`
	Running bool       `json:"running"`
}

// Info returns the schedule state for every configured job.
func (s *Scheduler) Info() []ScheduleInfo {
	cfg := s.store.Snapshot()

	s.mu.Lock()
	entries := make(map[string]cron.EntryID, len(s.entries))
	for k, v := range s.entries {
		entries[k] = v
	}
	invalid := make(map[string]string, len(s.invalid))
	for k, v := range s.invalid {
		invalid[k] = v
	}
	s.mu.Unlock()

	out := make([]ScheduleInfo, 0, len(cfg.Jobs))
	for i := range cfg.Jobs {
		job := &cfg.Jobs[i]
		info := ScheduleInfo{
			JobID:   job.ID,
			LastRun: job.LastRunAt,
			Running: s.runner.Running(job.ID),
			Error:   invalid[job.ID],
		}
		if spec, err := Spec(job); err == nil && spec != "" {
			info.Spec = spec
			if id, ok := entries[job.ID]; ok {
				if e := s.cron.Entry(id); !e.Next.IsZero() {
					next := e.Next
					info.NextRun = &next
					info.Active = true
				}
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	return out
}

// NextRun returns the next scheduled time for one job.
func (s *Scheduler) NextRun(jobID string) (time.Time, bool) {
	s.mu.Lock()
	id, ok := s.entries[jobID]
	s.mu.Unlock()
	if !ok {
		return time.Time{}, false
	}
	e := s.cron.Entry(id)
	if e.Next.IsZero() {
		return time.Time{}, false
	}
	return e.Next, true
}

// ValidateSpec checks an expression the user typed in the UI.
func ValidateSpec(mode, expr string) error {
	probe := &config.Job{Schedule: config.Schedule{Mode: mode, Cron: expr, Interval: expr}}
	_, err := Spec(probe)
	return err
}

// NextRunsFor returns the next n fire times of a specification, for previewing
// a schedule in the UI.
func NextRunsFor(mode, expr string, n int) ([]time.Time, error) {
	probe := &config.Job{Schedule: config.Schedule{Mode: mode, Cron: expr, Interval: expr}}
	spec, err := Spec(probe)
	if err != nil {
		return nil, err
	}
	if spec == "" {
		return nil, nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	sched, err := parser.Parse(spec)
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > 20 {
		n = 5
	}
	now := time.Now()
	out := make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		now = sched.Next(now)
		if now.IsZero() {
			break
		}
		out = append(out, now)
	}
	return out, nil
}

type cronLogger struct{ logf func(string, ...any) }

func (c cronLogger) Info(msg string, keysAndValues ...any) {
	c.logf("cron: %s %v", msg, keysAndValues)
}

func (c cronLogger) Error(err error, msg string, keysAndValues ...any) {
	c.logf("cron error: %s: %v %v", msg, err, keysAndValues)
}
