package wfdot

import (
	"strings"
	"testing"
)

// sty_73224b86: `[<step>.answer_progress] enabled / min_progress` on a propose
// step. Absent means off; a malformed table or one on a non-propose step is
// refused rather than silently doing nothing.

func withAnswerProgress(table string) string {
	return strings.Replace(readinessSteps, "reviewers = [\"gate-a\", \"gate-b\"]\nrequires = [\"raised\"]\n",
		"reviewers = [\"gate-a\", \"gate-b\"]\nrequires = [\"raised\"]\n\n[readied.answer_progress]\n"+table, 1)
}

func TestAnswerProgressParsesOntoStepAndState(t *testing.T) {
	for _, c := range []struct {
		name, table string
		on          bool
		min         int
	}{
		{"absent", "", false, 0},
		{"enabled, default min", "enabled = true\n", true, 1},
		{"enabled, min 2", "enabled = true\nmin_progress = 2\n", true, 2},
		{"disabled", "enabled = false\nmin_progress = 3\n", false, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			steps := readinessSteps
			if c.name != "absent" {
				steps = withAnswerProgress(c.table)
			}
			cat, err := ParseSteps(steps)
			if err != nil {
				t.Fatalf("ParseSteps: %v", err)
			}
			var step Step
			for _, st := range cat.Steps {
				if st.Provides == "readied" {
					step = st
				}
			}
			if step.AnswerProgress != c.on || step.MinProgress != c.min {
				t.Errorf("step = on %v min %d, want on %v min %d", step.AnswerProgress, step.MinProgress, c.on, c.min)
			}
			spec, err := ParseRoute(readinessDone, steps, "feature", nil)
			if err != nil {
				t.Fatalf("ParseRoute: %v", err)
			}
			st, ok := spec.StateNamed("plan")
			if !ok || st.AnswerProgress != c.on || st.MinProgress != c.min {
				t.Errorf("state = %+v, want on %v min %d", st, c.on, c.min)
			}
		})
	}
}

func TestAnswerProgressMalformedIsRefused(t *testing.T) {
	for _, c := range []struct {
		name, steps string
		want        []string
	}{
		{"min 0", withAnswerProgress("enabled = true\nmin_progress = 0\n"), []string{`step "readied"`, "min_progress must be >= 1"}},
		{"no enabled", withAnswerProgress("min_progress = 2\n"), []string{`step "readied"`, "needs enabled"}},
		{"unknown key", withAnswerProgress("enabled = true\nmin_progres = 2\n"), []string{"min_progres"}},
		{"non-propose step", strings.Replace(withAnswerProgress("enabled = true\n"), "propose = true\n", "", 1), []string{`step "readied"`, "answer_progress requires propose = true"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseSteps(c.steps)
			if err == nil {
				t.Fatal("want an error")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must contain %q, got: %v", want, err)
				}
			}
		})
	}
}
