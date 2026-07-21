package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"strings"
)

// twTask is the subset of a Taskwarrior export record the UI needs.
type twTask struct {
	UUID        string   `json:"uuid"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Tags        []string `json:"tags"`
	Urgency     float64  `json:"urgency"`
}

// listTasks returns pending tasks, most-urgent first.
// ponytail: runs synchronously in the UI loop; export is fast (<~100ms).
// Move to a tea.Cmd if a huge task db ever makes the picker stutter.
func listTasks() ([]twTask, error) {
	cmd := exec.Command("task", "rc.json.array=on", "status:pending", "export")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, taskErr(stderr.String(), err)
	}
	var ts []twTask
	if err := json.Unmarshal(out, &ts); err != nil {
		return nil, err
	}
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].Urgency > ts[j].Urgency })
	return ts, nil
}

func taskDone(uuid string) error { return runTask(uuid, "done") }

// taskStart/taskStop mirror the current-task selection into Taskwarrior's
// built-in active timer. `task done` clears an active timer on its own, so the
// done path needs no explicit stop.
func taskStart(uuid string) error { return runTask(uuid, "start") }
func taskStop(uuid string) error  { return runTask(uuid, "stop") }

// activeTasks returns the uuids Taskwarrior currently has running (+ACTIVE).
func activeTasks() ([]string, error) {
	cmd := exec.Command("task", "rc.json.array=on", "rc.verbose=nothing", "+ACTIVE", "export")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, taskErr(stderr.String(), err)
	}
	var ts []twTask
	if err := json.Unmarshal(out, &ts); err != nil {
		return nil, err
	}
	uuids := make([]string, len(ts))
	for i, t := range ts {
		uuids[i] = t.UUID
	}
	return uuids, nil
}

// taskReconcile decides how to make target the sole active task given TW's
// current active set: stop every active task that isn't target, and start
// target unless it's already active. Reconciling against TW's real state (not
// the model's last-known current) keeps reselection idempotent and heals drift
// left by a prior quit that kept a task active — `task start`/`stop` error
// (exit 1) when a task is already started / not started, so the guards matter.
func taskReconcile(active []string, target string) (stop []string, start bool) {
	start = true
	for _, u := range active {
		if u == target {
			start = false
		} else {
			stop = append(stop, u)
		}
	}
	return stop, start
}

// taskAdd/taskModify pass the raw text through so Taskwarrior parses its own
// inline syntax (project:x, +tag). Fields() splits into argv; TW rejoins.
func taskAdd(text string) error {
	return runTask(append([]string{"add"}, strings.Fields(text)...)...)
}

func taskModify(uuid, text string) error {
	return runTask(append([]string{uuid, "modify"}, strings.Fields(text)...)...)
}

func runTask(args ...string) error {
	// rc.confirmation=off: no tty is attached, so a confirmation prompt would
	// hang on EOF. rc.verbose=nothing: silence the "Configuration override"
	// footnote TW writes to stderr, so the real failure message (which TW writes
	// to stdout) is what surfaces to the UI.
	cmd := exec.Command("task", append([]string{"rc.confirmation=off", "rc.verbose=nothing"}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stdout.String())
		if msg == "" {
			msg = stderr.String()
		}
		return taskErr(msg, err)
	}
	return nil
}

func taskErr(stderr string, err error) error {
	if msg := strings.TrimSpace(stderr); msg != "" {
		return errors.New(msg)
	}
	return err
}
