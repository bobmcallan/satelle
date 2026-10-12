package serve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The substrate push leg (sty_a66e1846): each reconcile pass execs the config and
// documents push verbs after the work-state push.

// AC2: the loop runs exactly these two verbs, and never pulls, deploys, forces or
// prunes.
func TestSubstratePushArgvIsFixed(t *testing.T) {
	want := [][]string{
		{"sync", "config", "push", "--snapshot-only"},
		{"sync", "documents", "push", "--snapshot-only"},
	}
	if !reflect.DeepEqual(substratePushArgs, want) {
		t.Fatalf("substrate argv = %v, want %v", substratePushArgs, want)
	}
	for _, args := range substratePushArgs {
		for _, a := range args {
			switch a {
			case "--force", "--prune", "pull", "deploy", "rehydrate", "reconcile":
				t.Errorf("the unattended push passes %q: %v", a, args)
			}
		}
	}
}

// AC2: what is exec'd is that argv, in the repo, one child per verb; a refusal
// in the first does not stop the second and both surface in the error.
func TestSubstratePushViaCLIExecsEachVerbInTheRepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the satelle binary")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.log")
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$PWD\" \"$*\" >> " + log + "\n" +
		"case \"$2\" in config) echo 'skills: behind' >&2; exit 3;; esac\n"
	if err := os.WriteFile(filepath.Join(bin, "satelle"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	repo := t.TempDir()

	err := substratePushViaCLI(context.Background(), repo)
	if err == nil || !strings.Contains(err.Error(), "skills: behind") {
		t.Fatalf("the config refusal did not surface: %v", err)
	}
	raw, rerr := os.ReadFile(log)
	if rerr != nil {
		t.Fatal(rerr)
	}
	resolved, _ := filepath.EvalSymlinks(repo)
	want := resolved + "|sync config push --snapshot-only\n" + resolved + "|sync documents push --snapshot-only\n"
	if got := string(raw); got != want {
		t.Fatalf("exec'd calls:\n%s\nwant:\n%s", got, want)
	}
}

// AC1: every pass invokes the substrate push for every target, after that
// target's work-state push.
func TestPassInvokesSubstratePushAfterPushForEveryTarget(t *testing.T) {
	var calls []string
	r := &Reconciler{
		Targets: func(context.Context) []string { return []string{"/repo/a", "/repo/b"} },
		Reseed:  func(context.Context, string) error { return nil },
		Push: func(_ context.Context, p string) error {
			calls = append(calls, "push "+p)
			return nil
		},
		SubstratePush: func(_ context.Context, p string) error {
			calls = append(calls, "substrate "+p)
			return nil
		},
	}
	r.pass(context.Background())
	r.pass(context.Background())
	want := []string{
		"push /repo/a", "substrate /repo/a", "push /repo/b", "substrate /repo/b",
		"push /repo/a", "substrate /repo/a", "push /repo/b", "substrate /repo/b",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

// AC5: the same substrate error on three passes is one log line, and a changed
// reason and a recovery are still reported.
func TestSubstrateFailureIsLoggedOncePerStreak(t *testing.T) {
	logs := &lineRecorder{}
	var err error
	r := &Reconciler{
		Targets:       func(context.Context) []string { return []string{"/repo"} },
		Reseed:        func(context.Context, string) error { return nil },
		SubstratePush: func(context.Context, string) error { return err },
		Log:           logs.log,
	}
	err = errors.New("sync config push: snapshot-only: snapshots unavailable: HTTP 500")
	for i := 0; i < 3; i++ {
		r.pass(context.Background())
	}
	if n := countLines(logs.snapshot(), "/repo", "HTTP 500"); n != 1 {
		t.Fatalf("three identical substrate failures logged %d lines, want 1: %v", n, logs.snapshot())
	}

	err = errors.New("sync config push: skills: behind")
	r.pass(context.Background())
	if n := countLines(logs.snapshot(), "skills: behind"); n != 1 {
		t.Fatalf("a changed reason must be logged: %v", logs.snapshot())
	}

	err = nil
	r.pass(context.Background())
	r.pass(context.Background())
	if n := countLines(logs.snapshot(), "recovered"); n != 1 {
		t.Fatalf("recovery logged %d times, want 1: %v", n, logs.snapshot())
	}
}

// A substrate refusal is not a snapshot-leg failure: it must not reach Record,
// and the work-state streak keeps its own log line.
func TestSubstrateFailureDoesNotReachSnapshotRecord(t *testing.T) {
	var recorded []string
	logs := &lineRecorder{}
	r := &Reconciler{
		Targets:       func(context.Context) []string { return []string{"/repo"} },
		Reseed:        func(context.Context, string) error { return nil },
		SubstratePush: func(context.Context, string) error { return errors.New("refused") },
		Record: func(path string, success bool, reason string) {
			recorded = append(recorded, path+":"+reason)
		},
		Log: logs.log,
	}
	r.pass(context.Background())
	if len(recorded) != 1 || recorded[0] != "/repo:" {
		t.Fatalf("the snapshot leg recorded %v, want one success", recorded)
	}
}

// AC6: with no substrate leg the pass logs nothing, and a repo that leaves the
// targets takes its substrate streak with it.
func TestNilSubstrateLegLogsNothing(t *testing.T) {
	logs := &lineRecorder{}
	r := &Reconciler{
		Targets: func(context.Context) []string { return []string{"/repo"} },
		Reseed:  func(context.Context, string) error { return nil },
		Log:     logs.log,
	}
	r.pass(context.Background())
	r.pass(context.Background())
	if lines := logs.snapshot(); len(lines) != 0 {
		t.Fatalf("a nil substrate leg logged %v", lines)
	}
}

func TestSubstrateStreakIsForgottenWhenRepoLeavesTargets(t *testing.T) {
	targets := []string{"/repo"}
	r := &Reconciler{
		Targets:       func(context.Context) []string { return targets },
		Reseed:        func(context.Context, string) error { return nil },
		SubstratePush: func(context.Context, string) error { return errors.New("refused") },
	}
	r.pass(context.Background())
	if len(r.failing) != 1 {
		t.Fatalf("failing = %v, want the substrate streak", r.failing)
	}
	targets = nil
	r.pass(context.Background())
	if len(r.failing) != 0 {
		t.Fatalf("a repo that left the targets kept a streak: %v", r.failing)
	}
}
