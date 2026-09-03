// Package scheduler implements the built-in, always-running background
// task scheduler described in AI.md PART 18 "Scheduler": persistent
// per-task state in server.db, startup catch-up for missed runs, retry-free
// synchronous execution (retry policy is left to each TaskFunc), and audit
// logging to log files only — the scheduler never prints to the console
// (AI.md PART 18 "Task activity is logged to log files only").
//
// The in-process job engine is github.com/go-co-op/gocron/v2, per AI.md
// PART 5 "Utilities" dependency table.
package scheduler

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/notify"
)

// TaskFunc is the work a scheduled task performs. A nil return means
// success; any other error is recorded as a failed run and logged.
type TaskFunc func(ctx context.Context) error

// TaskDef describes one built-in scheduled task, per AI.md PART 18
// "Built-in Tasks (Required)".
type TaskDef struct {
	ID       string
	Name     string
	Schedule string
	Enabled  bool
	Run      TaskFunc
}

// Scheduler wraps gocron's in-process scheduler with the DB-backed
// persistent state, startup catch-up, and audit logging AI.md PART 18
// requires.
type Scheduler struct {
	sqlDB         *sql.DB
	logger        *applog.Logger
	gs            gocron.Scheduler
	catchUpWindow time.Duration

	mu    sync.Mutex
	tasks map[string]*TaskDef
	jobs  map[string]gocron.Job

	metrics *metrics.Metrics
	// notifier raises the AI.md PART 17 `scheduler_error` email event on a
	// failed run. Nil-safe: notify.Notifier's methods are inert on nil.
	notifier *notify.Notifier
}

// SetMetrics attaches m so future task runs are recorded to the
// scheduler_* metrics (AI.md PART 20 "Scheduler Metrics"). m may be nil
// (metrics disabled), in which case runTask skips recording. Not part of
// New's signature so existing tests/call sites are unaffected.
func (s *Scheduler) SetMetrics(m *metrics.Metrics) {
	s.metrics = m
}

// New creates a Scheduler backed by sqlDB for persistent state and logger
// for task-activity logging. timezone is an IANA name (AI.md PART 18
// "Timezone for scheduled tasks"); an invalid/empty value falls back to
// UTC rather than failing startup.
func New(sqlDB *sql.DB, logger *applog.Logger, timezone, catchUpWindow string) (*Scheduler, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil || timezone == "" {
		loc = time.UTC
	}
	window, err := time.ParseDuration(catchUpWindow)
	if err != nil {
		window = time.Hour
	}
	gs, err := gocron.NewScheduler(gocron.WithLocation(loc))
	if err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}
	return &Scheduler{
		sqlDB:         sqlDB,
		logger:        logger,
		gs:            gs,
		catchUpWindow: window,
		tasks:         map[string]*TaskDef{},
		jobs:          map[string]gocron.Job{},
	}, nil
}

// Register adds a task definition and persists/refreshes its row in
// scheduler_tasks. Call before Start; Start schedules every registered
// task with gocron and, per AI.md PART 18 "Automatic Recovery", runs any
// task whose persisted next_run is overdue but still within the catch-up
// window immediately.
func (s *Scheduler) Register(ctx context.Context, t TaskDef) error {
	if t.ID == "" || t.Run == nil {
		return fmt.Errorf("scheduler: task %q missing id or run func", t.ID)
	}
	s.mu.Lock()
	s.tasks[t.ID] = &t
	s.mu.Unlock()
	return db.UpsertSchedulerTask(ctx, s.sqlDB, t.ID, t.Name, t.Schedule)
}

// Start schedules every registered task and starts the gocron loop. It
// does not block.
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, t := range s.tasks {
		jobDef, err := parseSchedule(t.Schedule)
		if err != nil {
			return fmt.Errorf("scheduler: task %s: %w", id, err)
		}
		taskID := id
		job, err := s.gs.NewJob(jobDef, gocron.NewTask(func() { s.runTask(taskID) }), gocron.WithName(id))
		if err != nil {
			return fmt.Errorf("scheduler: schedule task %s: %w", id, err)
		}
		s.jobs[id] = job
	}

	s.catchUp(ctx)
	s.gs.Start()
	return nil
}

// catchUp runs, once and synchronously-in-goroutines, every enabled task
// whose persisted next_run has already passed but is still within the
// catch-up window, per AI.md PART 18 "Startup Behavior".
func (s *Scheduler) catchUp(ctx context.Context) {
	now := time.Now()
	for id := range s.tasks {
		row, err := db.GetSchedulerTask(ctx, s.sqlDB, id)
		if err != nil || row == nil || row.NextRun == nil || !row.Enabled {
			continue
		}
		overdue := now.Sub(*row.NextRun)
		if overdue > 0 && overdue <= s.catchUpWindow {
			go s.runTask(id)
		}
	}
}

// Stop performs the AI.md PART 18 "Shutdown Behavior" sequence: stop
// accepting new runs and wait (bounded by ctx) for gocron to drain.
func (s *Scheduler) Stop() error {
	return s.gs.Shutdown()
}

// runTask executes one task by id, recording its outcome to scheduler_tasks
// / scheduler_history and logging to the application log (never the
// console). Disabled tasks are recorded as "skipped" without running.
func (s *Scheduler) runTask(id string) {
	ctx := context.Background()
	s.mu.Lock()
	t := s.tasks[id]
	job := s.jobs[id]
	s.mu.Unlock()
	if t == nil {
		return
	}

	row, err := db.GetSchedulerTask(ctx, s.sqlDB, id)
	enabled := t.Enabled
	if err == nil && row != nil {
		enabled = row.Enabled
	}

	if s.metrics != nil {
		s.metrics.SchedulerTasksRunning.WithLabelValues(id).Inc()
	}
	started := time.Now()
	status := "skipped"
	errMsg := ""
	if enabled {
		if runErr := t.Run(ctx); runErr != nil {
			status = "failed"
			errMsg = runErr.Error()
		} else {
			status = "success"
		}
	}
	finished := time.Now()
	if s.metrics != nil {
		s.metrics.SchedulerTasksRunning.WithLabelValues(id).Dec()
		if status != "skipped" {
			metricsStatus := status
			if metricsStatus == "failed" {
				metricsStatus = "error"
			}
			s.metrics.SchedulerTasksTotal.WithLabelValues(id, metricsStatus).Inc()
			s.metrics.SchedulerTaskDuration.WithLabelValues(id).Observe(finished.Sub(started).Seconds())
		}
		s.metrics.SchedulerLastRunTimestamp.WithLabelValues(id).Set(float64(finished.Unix()))
	}

	var next *time.Time
	if job != nil {
		if nr, err := job.NextRun(); err == nil {
			next = &nr
		}
	}

	if status == "failed" {
		s.notifySchedulerError(id, t.Name, errMsg, next)
	}

	if err := db.RecordSchedulerRun(ctx, s.sqlDB, id, started, finished, next, status, errMsg); err != nil && s.logger != nil {
		_ = s.logger.WriteLine(applog.LevelError, fmt.Sprintf("scheduler: record run for %s: %v", id, err))
	}
	if s.logger == nil {
		return
	}
	switch status {
	case "failed":
		_ = s.logger.WriteLine(applog.LevelError, fmt.Sprintf("scheduler: task %s failed after %s: %s", id, finished.Sub(started), errMsg))
	case "skipped":
		_ = s.logger.WriteLine(applog.LevelInfo, fmt.Sprintf("scheduler: task %s skipped (disabled)", id))
	default:
		_ = s.logger.WriteLine(applog.LevelInfo, fmt.Sprintf("scheduler: task %s completed in %s", id, finished.Sub(started)))
	}
}

// RunNow executes task id immediately, ignoring its schedule and enabled
// flag (an explicit manual trigger always runs — AI.md PART 18 "Run Now"),
// and blocks until it completes.
func (s *Scheduler) RunNow(ctx context.Context, id string) error {
	s.mu.Lock()
	t := s.tasks[id]
	s.mu.Unlock()
	if t == nil {
		return fmt.Errorf("scheduler: unknown task %q", id)
	}

	started := time.Now()
	runErr := t.Run(ctx)
	finished := time.Now()
	status := "success"
	errMsg := ""
	if runErr != nil {
		status = "failed"
		errMsg = runErr.Error()
	}

	var next *time.Time
	s.mu.Lock()
	job := s.jobs[id]
	s.mu.Unlock()
	if job != nil {
		if nr, err := job.NextRun(); err == nil {
			next = &nr
		}
	}
	if err := db.RecordSchedulerRun(ctx, s.sqlDB, id, started, finished, next, status, errMsg); err != nil {
		return err
	}
	return runErr
}

// SetEnabled toggles task id's enabled flag in persistent state.
func (s *Scheduler) SetEnabled(ctx context.Context, id string, enabled bool) error {
	s.mu.Lock()
	_, ok := s.tasks[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("scheduler: unknown task %q", id)
	}
	return db.SetSchedulerTaskEnabled(ctx, s.sqlDB, id, enabled)
}

// List returns the persisted state of every registered task.
func (s *Scheduler) List(ctx context.Context) ([]db.SchedulerTask, error) {
	return db.ListSchedulerTasks(ctx, s.sqlDB)
}

// Show returns the persisted state of a single task.
func (s *Scheduler) Show(ctx context.Context, id string) (*db.SchedulerTask, error) {
	return db.GetSchedulerTask(ctx, s.sqlDB, id)
}

// History returns the most recent runs of task id, newest first.
func (s *Scheduler) History(ctx context.Context, id string, limit int) ([]db.SchedulerHistoryEntry, error) {
	return db.SchedulerHistory(ctx, s.sqlDB, id, limit)
}

// parseSchedule converts an AI.md PART 18 "Schedule Format" string into a
// gocron.JobDefinition: a raw 5-field cron expression, one of the
// `@hourly`/`@daily`/`@weekly`/`@monthly` cron aliases, or `@every
// <duration>` for interval jobs.
func parseSchedule(schedule string) (gocron.JobDefinition, error) {
	switch schedule {
	case "@hourly":
		return gocron.CronJob("0 * * * *", false), nil
	case "@daily":
		return gocron.CronJob("0 0 * * *", false), nil
	case "@weekly":
		return gocron.CronJob("0 0 * * 0", false), nil
	case "@monthly":
		return gocron.CronJob("0 0 1 * *", false), nil
	}
	if rest, ok := strings.CutPrefix(schedule, "@every "); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("invalid @every duration %q: %w", rest, err)
		}
		return gocron.DurationJob(d), nil
	}
	return gocron.CronJob(schedule, false), nil
}
