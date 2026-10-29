//go:build integration

package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestTaskTriggerFromFixtureConfigurationRunsOneLocalExecution fires a trigger
// declared only in a fixture repo's task file: the trigger's name, the status it
// counts and its threshold appear nowhere but in that file. run-once runs nothing
// below the threshold, and at it makes one execution of the task that records
// the trigger and its source, driven locally to the end of the task's route.
func TestTaskTriggerFromFixtureConfigurationRunsOneLocalExecution(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	stubReviewerAccept(t, repo)
	// A headless executor, so the run is driven with no session at a prompt.
	executor := filepath.Join(t.TempDir(), "executor.sh")
	if err := os.WriteFile(executor, []byte("#!/bin/sh\necho done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agents := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	f, err := os.OpenFile(agents, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(f, "\n[executor]\ncommand = %q\ntools = \"Bash(satelle:*)\"\n", executor+" {system}")
	_ = f.Close()

	tasks := filepath.Join(repo, ".satelle", "tasks")
	if err := os.MkdirAll(tasks, 0o755); err != nil {
		t.Fatal(err)
	}
	task := "---\nid: tsk_release\ntype: task\nstatus: backlog\n" +
		"inputs_decl: trigger:string, trigger_source:string\n" +
		"triggers: release=count:awaiting_merge>=2\n" +
		"---\n\n# Release\n\nACTION: merge and release. VERIFICATION: released.\n"
	if err := os.WriteFile(filepath.Join(tasks, "tsk_release.md"), []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, testBin, repo, "reindex")

	waiting := func(title string) {
		mustRun(t, testBin, repo, "story", "create", "--title", title, "--body", "Waits to be merged.", "--status", "awaiting_merge")
	}
	executions := func() []struct {
		ID     string            `json:"id"`
		Status string            `json:"status"`
		Tags   []string          `json:"tags"`
		Inputs map[string]string `json:"inputs"`
	} {
		var out []struct {
			ID     string            `json:"id"`
			Status string            `json:"status"`
			Tags   []string          `json:"tags"`
			Inputs map[string]string `json:"inputs"`
		}
		raw := mustRun(t, testBin, repo, "execution", "list", "--parent", "tsk_release", "--json")
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatalf("execution list: %v\n%s", err, raw)
		}
		return out
	}

	waiting("First")
	if out := mustRun(t, testBin, repo, "task", "trigger", "run-once"); !strings.Contains(out, "no trigger fired") {
		t.Errorf("one story waiting: run-once said\n%s\nwant no trigger fired", out)
	}
	if got := executions(); len(got) != 0 {
		t.Fatalf("one story waiting made executions %+v, want none", got)
	}

	waiting("Second")
	out := mustRun(t, testBin, repo, "task", "trigger", "run-once")
	if !strings.Contains(out, "fired tsk_release trigger release=count:awaiting_merge>=2") || !strings.Contains(out, "done (terminal)") {
		t.Errorf("run-once said\n%s\nwant the release trigger fired and its run driven to done", out)
	}
	got := executions()
	if len(got) != 1 {
		t.Fatalf("executions = %+v, want exactly one", got)
	}
	if want := map[string]string{"trigger": "release", "trigger_source": "count"}; !reflect.DeepEqual(got[0].Inputs, want) {
		t.Errorf("inputs = %v, want %v", got[0].Inputs, want)
	}
	if !containsTag(got[0].Tags, "trigger:tsk_release:release") || got[0].Status != "done" {
		t.Errorf("execution = %+v, want tagged trigger:tsk_release:release at done", got[0])
	}
}

func containsTag(tags []string, want string) bool {
	for _, tg := range tags {
		if tg == want {
			return true
		}
	}
	return false
}
