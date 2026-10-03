package wfdot

import (
	"strings"
	"testing"
)

// A container step's `schedule` is a closed declaration, authored in step.toml.
// The binary stores it as authored — absent stays absent — and never reads it
// as reviewer fan-out.

func scheduleSteps(extra string) string {
	return `[raised]
status = "backlog"
start = true

[ready]
status = "ready"
agent = "ready-reviewer"
waits_on_children = true
requires = ["raised"]
` + extra + `
[children-resolved]
status = "done"
agent = "reviewer"
terminal = true
requires = ["raised"]
`
}

func TestScheduleAcceptsTheClosedSet(t *testing.T) {
	for _, v := range []string{SchedParallel, SchedSequential} {
		cat, err := ParseSteps(scheduleSteps(`schedule = "` + v + `"`))
		if err != nil {
			t.Fatalf("schedule %q: %v", v, err)
		}
		if got := stepProviding(t, cat, "ready").Schedule; got != v {
			t.Errorf("schedule = %q, want %q", got, v)
		}
		spec, err := ParseRoute(waitsDone, scheduleSteps(`schedule = "`+v+`"`), "epic-parent", nil)
		if err != nil {
			t.Fatalf("ParseRoute: %v", err)
		}
		if st, _ := spec.StateNamed("ready"); st.Schedule != v {
			t.Errorf("State.Schedule = %q, want %q", st.Schedule, v)
		}
	}
}

func TestScheduleRefusesAValueOutsideTheSet(t *testing.T) {
	_, err := ParseSteps(scheduleSteps(`schedule = "burst"`))
	if err == nil {
		t.Fatal("schedule = burst must be refused")
	}
	for _, want := range []string{"schedule", `"burst"`, SchedParallel, SchedSequential, `"ready"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q must contain %q", err, want)
		}
	}
}

func TestScheduleRefusedOnAStepThatDoesNotWaitOnChildren(t *testing.T) {
	body := strings.Replace(scheduleSteps(`schedule = "sequential"`), "waits_on_children = true\n", "", 1)
	_, err := ParseSteps(body)
	if err == nil || !strings.Contains(err.Error(), "waits_on_children") || !strings.Contains(err.Error(), `"ready"`) {
		t.Fatalf("a schedule on a non-container step must be refused naming the step: %v", err)
	}
}

func TestAbsentScheduleStaysAbsent(t *testing.T) {
	cat, err := ParseSteps(scheduleSteps(""))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	got := stepProviding(t, cat, "ready").Schedule
	if got != "" || got == SchedParallel || got == SchedSequential {
		t.Errorf("absent schedule = %q, want empty (neither parallel nor sequential)", got)
	}
}

func TestScheduleAndReviewerParallelAreTwoFields(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		wantPar     int
		wantSched   string
	}{
		{"three", "schedule = \"sequential\"\nparallel = 3", 3, SchedSequential},
		{"zero", "schedule = \"parallel\"\nparallel = 0", 0, SchedParallel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cat, err := ParseSteps(scheduleSteps(tc.extra))
			if err != nil {
				t.Fatalf("ParseSteps: %v", err)
			}
			st := stepProviding(t, cat, "ready")
			if st.Schedule != tc.wantSched || st.Parallel != tc.wantPar || !st.ParallelSet {
				t.Errorf("Schedule=%q Parallel=%d ParallelSet=%v, want %q/%d/true", st.Schedule, st.Parallel, st.ParallelSet, tc.wantSched, tc.wantPar)
			}
		})
	}
	cat, err := ParseSteps(scheduleSteps(`schedule = "sequential"`))
	if err != nil {
		t.Fatal(err)
	}
	if st := stepProviding(t, cat, "ready"); st.ParallelSet || st.Parallel != 0 {
		t.Errorf("schedule alone must not set reviewer fan-out: Parallel=%d ParallelSet=%v", st.Parallel, st.ParallelSet)
	}
}
