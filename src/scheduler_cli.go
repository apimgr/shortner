// --scheduler command handling. See AI.md PART 18 "CLI Commands": list,
// show <id>, run <id>, enable <id>, disable <id>, history <id>. This
// reuses the existing PART 8 flag-based subcommand pattern (see
// --maintenance/--service/--update) — no new bare CLI subcommand is added.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/apimgr/shortner/src/scheduler"
)

// schedulerHelp is the --scheduler --help text.
const schedulerHelp = `Usage: %s --scheduler COMMAND [ID]

Manage the built-in background task scheduler.

Commands:
  list             List all tasks and their status
  show <id>        Show task detail and recent history
  run <id>         Run a task immediately
  enable <id>      Enable a task
  disable <id>     Disable a task
  history <id>     Show a task's execution history
  --help           Show this help
`

// runSchedulerCLI dispatches --scheduler COMMAND [ID] against sched and
// returns the process exit code.
func runSchedulerCLI(binaryName string, sched *scheduler.Scheduler, command, arg string) int {
	switch command {
	case "", "help", "--help", "-h":
		fmt.Printf(schedulerHelp, binaryName)
		return 0
	}

	ctx := context.Background()

	switch command {
	case "list":
		return schedulerList(ctx, binaryName, sched)
	case "show":
		return schedulerShow(ctx, binaryName, sched, arg)
	case "run":
		return schedulerRun(ctx, binaryName, sched, arg)
	case "enable":
		return schedulerSetEnabled(ctx, binaryName, sched, arg, true)
	case "disable":
		return schedulerSetEnabled(ctx, binaryName, sched, arg, false)
	case "history":
		return schedulerHistory(ctx, binaryName, sched, arg)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown --scheduler command %q (run '%s --scheduler --help')\n", binaryName, command, binaryName)
		return 1
	}
}

// statusIcon maps a scheduler_tasks.last_status value to the AI.md PART 18
// "Legend" glyph (success/failed/skipped/pending).
func statusIcon(status string) string {
	switch status {
	case "success":
		return "✓" // check mark
	case "failed":
		return "✗" // ballot x
	case "skipped":
		return "◐" // half circle
	default:
		return "○" // pending
	}
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

func schedulerList(ctx context.Context, binaryName string, sched *scheduler.Scheduler) int {
	tasks, err := sched.List(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	fmt.Printf("%-20s %-16s %-20s %-20s %s\n", "TASK", "SCHEDULE", "LAST RUN", "NEXT RUN", "STATUS")
	for _, t := range tasks {
		fmt.Printf("%-20s %-16s %-20s %-20s %s %s\n",
			t.Name, t.Schedule, formatTime(t.LastRun), formatTime(t.NextRun),
			statusIcon(t.LastStatus), enabledLabel(t.Enabled))
	}
	return 0
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "(enabled)"
	}
	return "(disabled)"
}

func schedulerShow(ctx context.Context, binaryName string, sched *scheduler.Scheduler, id string) int {
	if id == "" {
		fmt.Fprintf(os.Stderr, "%s: --scheduler show requires a task ID\n", binaryName)
		return 2
	}
	t, err := sched.Show(ctx, id)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if t == nil {
		fmt.Fprintf(os.Stderr, "%s: unknown task %q\n", binaryName, id)
		return 1
	}
	fmt.Printf("Task:        %s (%s)\n", t.Name, t.ID)
	fmt.Printf("Status:      %s %s\n", statusIcon(t.LastStatus), enabledLabel(t.Enabled))
	fmt.Printf("Schedule:    %s\n", t.Schedule)
	fmt.Printf("Last Run:    %s\n", formatTime(t.LastRun))
	fmt.Printf("Next Run:    %s\n", formatTime(t.NextRun))
	fmt.Printf("Run Count:   %d successful, %d failed\n", t.RunCount, t.FailCount)
	if t.LastError != "" {
		fmt.Printf("Last Error:  %s\n", t.LastError)
	}

	history, err := sched.History(ctx, id, 5)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if len(history) > 0 {
		fmt.Println("\nRecent History:")
		for _, h := range history {
			fmt.Printf("  %s  %s  %s\n", formatTime(&h.StartedAt), statusIcon(h.Status), h.Error)
		}
	}
	return 0
}

func schedulerRun(ctx context.Context, binaryName string, sched *scheduler.Scheduler, id string) int {
	if id == "" {
		fmt.Fprintf(os.Stderr, "%s: --scheduler run requires a task ID\n", binaryName)
		return 2
	}
	if err := sched.RunNow(ctx, id); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": task failed: "+err.Error())
		return 1
	}
	fmt.Printf("%s: task %q completed successfully\n", binaryName, id)
	return 0
}

func schedulerSetEnabled(ctx context.Context, binaryName string, sched *scheduler.Scheduler, id string, enabled bool) int {
	if id == "" {
		fmt.Fprintf(os.Stderr, "%s: --scheduler enable/disable requires a task ID\n", binaryName)
		return 2
	}
	if err := sched.SetEnabled(ctx, id, enabled); err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	verb := "enabled"
	if !enabled {
		verb = "disabled"
	}
	fmt.Printf("%s: task %q %s\n", binaryName, id, verb)
	return 0
}

func schedulerHistory(ctx context.Context, binaryName string, sched *scheduler.Scheduler, id string) int {
	if id == "" {
		fmt.Fprintf(os.Stderr, "%s: --scheduler history requires a task ID\n", binaryName)
		return 2
	}
	history, err := sched.History(ctx, id, 20)
	if err != nil {
		fmt.Fprintln(os.Stderr, binaryName+": "+err.Error())
		return 1
	}
	if len(history) == 0 {
		fmt.Printf("%s: no history for task %q\n", binaryName, id)
		return 0
	}
	fmt.Printf("%-20s %-10s %-12s %s\n", "STARTED", "STATUS", "DURATION", "ERROR")
	for _, h := range history {
		duration := "-"
		if h.FinishedAt != nil {
			duration = h.FinishedAt.Sub(h.StartedAt).String()
		}
		fmt.Printf("%-20s %-10s %-12s %s\n", formatTime(&h.StartedAt), h.Status, duration, h.Error)
	}
	return 0
}
