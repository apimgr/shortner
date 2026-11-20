// Package scheduler implements the built-in, always-running background
// scheduler described in AI.md PART 18, using only Go's time package.
package scheduler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/apimgr/shortner/src/applog"
	"github.com/apimgr/shortner/src/config"
	"github.com/apimgr/shortner/src/db"
	"github.com/apimgr/shortner/src/metrics"
	"github.com/apimgr/shortner/src/notify"
)

type TaskFunc func(ctx context.Context) error

type TaskDef struct {
	ID          string
	Name        string
	Schedule    string
	Enabled     bool
	RetryOnFail bool
	RetryDelay  time.Duration
	Run         TaskFunc
}

type Scheduler struct {
	sqlDB         db.Handle
	logger        *applog.Logger
	location      *time.Location
	catchUpWindow time.Duration

	mu       sync.Mutex
	tasks    map[string]*TaskDef
	nextRuns map[string]time.Time
	running  map[string]*taskRun
	started  bool
	stopping bool
	cancel   context.CancelFunc
	loopDone chan struct{}
	metrics  *metrics.Metrics
	notifier *notify.Notifier
}

type taskRun struct {
	cancel      context.CancelFunc
	startedAt   time.Time
	interrupted bool
}

func (s *Scheduler) SetMetrics(m *metrics.Metrics) { s.metrics = m }

// Healthy reports whether the scheduler loop has started and is not stopping.
func (s *Scheduler) Healthy() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started && !s.stopping
}

func New(sqlDB db.Handle, logger *applog.Logger, timezone, catchUpWindow string) (*Scheduler, error) {
	defaultTimezone := config.Default("").Server.Scheduler.Timezone
	if defaultTimezone == "" {
		defaultTimezone = "UTC"
	}
	configuredTimezone := timezone
	if timezone == "" {
		timezone = defaultTimezone
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc, err = time.LoadLocation(defaultTimezone)
		if err != nil {
			loc = time.UTC
			if logger != nil {
				_ = logger.WriteLine(applog.LevelWarn, "scheduler: invalid server.scheduler.timezone and default; using UTC\n")
			}
		} else if configuredTimezone != "" && logger != nil {
			_ = logger.WriteLine(applog.LevelWarn, fmt.Sprintf("scheduler: invalid server.scheduler.timezone; using default %s\n", defaultTimezone))
		}
	}
	window, err := time.ParseDuration(catchUpWindow)
	if err != nil || window <= 0 {
		window = time.Hour
	}
	return &Scheduler{
		sqlDB: sqlDB, logger: logger, location: loc, catchUpWindow: window,
		tasks: make(map[string]*TaskDef), nextRuns: make(map[string]time.Time),
		running: make(map[string]*taskRun), loopDone: make(chan struct{}),
	}, nil
}

func (s *Scheduler) Register(ctx context.Context, task TaskDef) error {
	if task.ID == "" || task.Run == nil {
		return fmt.Errorf("scheduler: task %q missing id or run func", task.ID)
	}
	if _, err := parseSchedule(task.Schedule); err != nil {
		return fmt.Errorf("scheduler: task %s: %w", task.ID, err)
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: cannot register task after start")
	}
	copy := task
	s.tasks[task.ID] = &copy
	s.mu.Unlock()
	return db.UpsertSchedulerTask(ctx, s.sqlDB, task.ID, task.Name, task.Schedule)
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	now := time.Now().In(s.location)
	for id, task := range s.tasks {
		row, err := db.GetSchedulerTask(ctx, s.sqlDB, id)
		if err != nil {
			return fmt.Errorf("scheduler: load task %s: %w", id, err)
		}
		next := nextScheduled(task.Schedule, now, s.location)
		if row != nil && row.NextRun != nil {
			persisted := row.NextRun.In(s.location)
			if persisted.After(now) {
				next = persisted
			} else if now.Sub(persisted) <= s.catchUpWindow && row.Enabled && task.Enabled {
				s.nextRuns[id] = next
				go s.runTask(id)
				continue
			}
		}
		s.nextRuns[id] = next
		if _, err := s.sqlDB.ExecContext(ctx, `UPDATE scheduler_tasks SET next_run = ? WHERE id = ?`, next.Unix(), id); err != nil {
			return fmt.Errorf("scheduler: persist next run for %s: %w", id, err)
		}
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started = true
	go s.loop(loopCtx)
	return nil
}

func (s *Scheduler) loop(ctx context.Context) {
	defer close(s.loopDone)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.fireDue(now.In(s.location))
		}
	}
}

func (s *Scheduler) fireDue(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return
	}
	for id, due := range s.nextRuns {
		if due.After(now) || s.running[id] != nil {
			continue
		}
		task := s.tasks[id]
		s.nextRuns[id] = nextScheduled(task.Schedule, now, s.location)
		_, _ = s.sqlDB.ExecContext(context.Background(), `UPDATE scheduler_tasks SET next_run = ? WHERE id = ?`, s.nextRuns[id].Unix(), id)
		go s.runTask(id)
	}
}

func (s *Scheduler) Stop() error {
	s.mu.Lock()
	if !s.started {
		s.stopping = true
		s.mu.Unlock()
		return nil
	}
	s.stopping = true
	cancel := s.cancel
	loopDone := s.loopDone
	s.mu.Unlock()
	cancel()
	<-loopDone

	drain := time.NewTimer(30 * time.Second)
	defer drain.Stop()
	for {
		s.mu.Lock()
		count := len(s.running)
		s.mu.Unlock()
		if count == 0 {
			return nil
		}
		select {
		case <-drain.C:
			s.forceReleaseInterrupted()
			return fmt.Errorf("scheduler: shutdown drain timed out; running tasks were cancelled and marked for retry")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (s *Scheduler) forceReleaseInterrupted() {
	now := time.Now()
	s.mu.Lock()
	ids := make([]string, 0, len(s.running))
	for id, run := range s.running {
		run.interrupted = true
		run.cancel()
		ids = append(ids, id)
		delete(s.running, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = db.RecordSchedulerRun(context.Background(), s.sqlDB, id, now, now, &now, "failed", "interrupted during shutdown; scheduled for retry")
	}
}

func (s *Scheduler) runTask(id string) {
	s.mu.Lock()
	task := s.tasks[id]
	if task == nil || s.stopping || s.running[id] != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &taskRun{cancel: cancel, startedAt: time.Now()}
	s.running[id] = run
	s.mu.Unlock()
	defer cancel()

	row, err := db.GetSchedulerTask(ctx, s.sqlDB, id)
	enabled := task.Enabled
	if err == nil && row != nil {
		enabled = enabled && row.Enabled
	}
	if s.metrics != nil {
		s.metrics.SchedulerTasksRunning.WithLabelValues(id).Inc()
		defer s.metrics.SchedulerTasksRunning.WithLabelValues(id).Dec()
	}
	status, errMsg := "skipped", ""
	if enabled {
		err := s.invokeWithRetry(ctx, task)
		status = "success"
		if err != nil {
			status, errMsg = "failed", err.Error()
		}
	}
	finished := time.Now()
	s.mu.Lock()
	interrupted := run.interrupted
	next := s.nextRuns[id]
	s.mu.Unlock()
	if interrupted {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
		return
	}
	var nextRun *time.Time
	if !next.IsZero() {
		nextRun = &next
	}
	if s.metrics != nil {
		if status != "skipped" {
			metricStatus := status
			if metricStatus == "failed" {
				metricStatus = "error"
			}
			s.metrics.SchedulerTasksTotal.WithLabelValues(id, metricStatus).Inc()
			s.metrics.SchedulerTaskDuration.WithLabelValues(id).Observe(finished.Sub(run.startedAt).Seconds())
		}
		s.metrics.SchedulerLastRunTimestamp.WithLabelValues(id).Set(float64(finished.Unix()))
	}
	switch status {
	case "failed":
		s.notifySchedulerError(id, task.Name, errMsg, nextRun)
	}
	if err := db.RecordSchedulerRun(context.Background(), s.sqlDB, id, run.startedAt, finished, nextRun, status, errMsg); err != nil && s.logger != nil {
		_ = s.logger.WriteLine(applog.LevelError, fmt.Sprintf("scheduler: record run for %s: %v\n", id, err))
	}
	if s.logger != nil {
		switch status {
		case "failed":
			level := applog.LevelWarn
			if task.RetryOnFail {
				level = applog.LevelError
			}
			_ = s.logger.WriteLine(level, fmt.Sprintf("scheduler: task %s failed after %s: %s\n", id, finished.Sub(run.startedAt), errMsg))
		case "skipped":
			_ = s.logger.WriteLine(applog.LevelDebug, fmt.Sprintf("scheduler: task %s skipped (disabled)\n", id))
		default:
			_ = s.logger.WriteLine(applog.LevelDebug, fmt.Sprintf("scheduler: task %s completed in %s\n", id, finished.Sub(run.startedAt)))
		}
	}
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

// invokeWithRetry applies PART 18's three-attempt exponential retry policy.
// Retry waits are cancellable so shutdown never waits out a backoff.
func (s *Scheduler) invokeWithRetry(ctx context.Context, task *TaskDef) error {
	var err error
	for attempt := 0; attempt <= 3; attempt++ {
		if attempt > 0 {
			delay := task.RetryDelay
			if delay <= 0 {
				delay = 5 * time.Minute
			}
			delay *= time.Duration(1 << (attempt - 1))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		err = invokeTask(ctx, task.Run)
		if err == nil || !task.RetryOnFail || attempt == 3 {
			return err
		}
	}
	return err
}

func invokeTask(ctx context.Context, fn TaskFunc) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("task panic: %v", value)
		}
	}()
	return fn(ctx)
}

func (s *Scheduler) RunNow(ctx context.Context, id string) error {
	s.mu.Lock()
	task := s.tasks[id]
	if task == nil {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: unknown task %q", id)
	}
	if s.stopping {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: stopping")
	}
	if s.running[id] != nil {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: task %q is already running", id)
	}
	runCtx, cancel := context.WithCancel(ctx)
	run := &taskRun{cancel: cancel, startedAt: time.Now()}
	s.running[id] = run
	s.mu.Unlock()
	defer cancel()

	err := s.invokeWithRetry(runCtx, task)
	finished := time.Now()
	s.mu.Lock()
	interrupted := run.interrupted
	next := s.nextRuns[id]
	s.mu.Unlock()
	if interrupted {
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
		return fmt.Errorf("scheduler: task %q interrupted during shutdown", id)
	}
	status, errMsg := "success", ""
	if err != nil {
		status, errMsg = "failed", err.Error()
	}
	var nextRun *time.Time
	if !next.IsZero() {
		nextRun = &next
	}
	recordErr := db.RecordSchedulerRun(context.Background(), s.sqlDB, id, run.startedAt, finished, nextRun, status, errMsg)
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
	if recordErr != nil {
		return recordErr
	}
	return err
}

func (s *Scheduler) SetEnabled(ctx context.Context, id string, enabled bool) error {
	s.mu.Lock()
	_, ok := s.tasks[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("scheduler: unknown task %q", id)
	}
	return db.SetSchedulerTaskEnabled(ctx, s.sqlDB, id, enabled)
}

func (s *Scheduler) List(ctx context.Context) ([]db.SchedulerTask, error) {
	return db.ListSchedulerTasks(ctx, s.sqlDB)
}

func (s *Scheduler) Show(ctx context.Context, id string) (*db.SchedulerTask, error) {
	return db.GetSchedulerTask(ctx, s.sqlDB, id)
}

func (s *Scheduler) History(ctx context.Context, id string, limit int) ([]db.SchedulerHistoryEntry, error) {
	return db.SchedulerHistory(ctx, s.sqlDB, id, limit)
}

type scheduleSpec struct {
	interval time.Duration
	fields   [5]cronField
}

type cronField map[int]bool

func parseSchedule(value string) (scheduleSpec, error) {
	switch value {
	case "@hourly":
		value = "0 * * * *"
	case "@daily":
		value = "0 0 * * *"
	case "@weekly":
		value = "0 0 * * 0"
	case "@monthly":
		value = "0 0 1 * *"
	}
	if strings.HasPrefix(value, "@every ") {
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(value, "@every ")))
		if err != nil || d <= 0 {
			return scheduleSpec{}, fmt.Errorf("invalid interval schedule %q", value)
		}
		return scheduleSpec{interval: d}, nil
	}
	parts := strings.Fields(value)
	if len(parts) != 5 {
		return scheduleSpec{}, fmt.Errorf("schedule %q must have five cron fields", value)
	}
	limits := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	var spec scheduleSpec
	for i, part := range parts {
		field, err := parseCronField(part, limits[i][0], limits[i][1])
		if err != nil {
			return scheduleSpec{}, fmt.Errorf("invalid cron field %q: %w", part, err)
		}
		spec.fields[i] = field
	}
	return spec, nil
}

func parseCronField(value string, min, max int) (cronField, error) {
	result := make(cronField)
	for _, item := range strings.Split(value, ",") {
		base, stepText, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			parsed, err := strconv.Atoi(stepText)
			if err != nil || parsed < 1 {
				return nil, fmt.Errorf("invalid step %q", stepText)
			}
			step = parsed
		}
		lo, hi := min, max
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			a, b, _ := strings.Cut(base, "-")
			var err error
			lo, err = strconv.Atoi(a)
			if err != nil {
				return nil, err
			}
			hi, err = strconv.Atoi(b)
			if err != nil {
				return nil, err
			}
		case base != "":
			v, err := strconv.Atoi(base)
			if err != nil {
				return nil, err
			}
			lo, hi = v, v
		default:
			return nil, fmt.Errorf("empty value")
		}
		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("value outside %d..%d", min, max)
		}
		for n := lo; n <= hi; n += step {
			result[n] = true
		}
	}
	return result, nil
}

func nextScheduled(expression string, from time.Time, loc *time.Location) time.Time {
	spec, err := parseSchedule(expression)
	if err != nil {
		return from.Add(time.Minute)
	}
	if spec.interval > 0 {
		return from.Add(spec.interval)
	}
	candidate := from.Truncate(time.Minute).Add(time.Minute)
	limit := candidate.AddDate(5, 0, 0)
	for candidate.Before(limit) {
		local := candidate.In(loc)
		if spec.fields[0][local.Minute()] && spec.fields[1][local.Hour()] && spec.fields[2][local.Day()] && spec.fields[3][int(local.Month())] && spec.fields[4][int(local.Weekday())] {
			return candidate
		}
		candidate = candidate.Add(time.Minute)
	}
	return from.Add(24 * time.Hour)
}
