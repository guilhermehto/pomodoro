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
	// hang on EOF.
	cmd := exec.Command("task", append([]string{"rc.confirmation=off"}, args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return taskErr(stderr.String(), err)
	}
	return nil
}

func taskErr(stderr string, err error) error {
	if msg := strings.TrimSpace(stderr); msg != "" {
		return errors.New(msg)
	}
	return err
}
