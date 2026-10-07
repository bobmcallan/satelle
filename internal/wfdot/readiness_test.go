package wfdot

import (
	"strings"
	"testing"
)

// sty_5262592e: one readiness step. The route declares three step knobs
// (propose, freeze, reject_budget) and a second recover edge (plan→backlog);
// nothing here names this repo's steps as more than fixture data.

const readinessSteps = `[raised]
status = "backlog"
start = true

[readied]
status = "plan"
agent = "planner"
skills = ["plan"]
propose = true
reject_budget = 3
reviewers = ["gate-a", "gate-b"]
requires = ["raised"]

[coded]
status = "in_progress"
agent = "coder"
skills = ["coder"]
freeze = true
requires = ["readied"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

const readinessDone = `["*"]
obligations = ["raised", "readied", "coded", "closed"]
park = { state = "blocked", gate = "park-gate" }
cancel = { state = "cancelled", gate = "cancel-gate" }
recover = [
  { step = "in_progress", from = ["done"] },
  { step = "backlog", from = ["plan"] },
]
`

func readinessSpec(t *testing.T) Spec {
	t.Helper()
	spec, err := ParseRoute(readinessDone, readinessSteps, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	return spec
}

func TestReadinessKnobsParseOntoStepAndState(t *testing.T) {
	cat, err := ParseSteps(readinessSteps)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "readied")
	if !st.Propose || st.RejectBudget != 3 {
		t.Errorf("readied = propose %v budget %d, want true / 3", st.Propose, st.RejectBudget)
	}
	if !stepProviding(t, cat, "coded").Freeze {
		t.Error("coded must carry freeze")
	}
	spec := readinessSpec(t)
	plan, ok := spec.StateNamed("plan")
	if !ok || !plan.Propose || plan.RejectBudget != 3 || plan.Freeze {
		t.Errorf("plan state = %+v (found %v)", plan, ok)
	}
	if coded, _ := spec.StateNamed("in_progress"); !coded.Freeze {
		t.Errorf("in_progress state must carry freeze: %+v", coded)
	}
}

// The derived route walks backlog → plan → in_progress with one readiness step
// (no `ready` state), and a rejected readiness edge has nowhere to go but back:
// the only forward edge out of backlog is the gated one into plan.
func TestReadinessRouteIsOneStepFromBacklog(t *testing.T) {
	spec := readinessSpec(t)
	if !spec.HasEdge("backlog", "plan") || !spec.HasEdge("plan", "in_progress") {
		t.Fatalf("route must declare backlog→plan→in_progress: %+v", spec.Transitions)
	}
	if _, ok := spec.StateNamed("ready"); ok {
		t.Error("the route must not carry a separate ready state")
	}
	for _, tr := range spec.Transitions {
		if tr.From == "backlog" && tr.To == "plan" {
			if got := strings.Join(tr.Skills, ","); got != "gate-a,gate-b" {
				t.Errorf("readiness reviewers gate the backlog→plan edge, got %q", got)
			}
		}
	}
}

// The recover array declares plan→backlog beside the original edge, and the
// entry state stays backlog even though it now has an incoming edge.
func TestRecoverArrayDeclaresBothBackwardEdges(t *testing.T) {
	spec := readinessSpec(t)
	if !spec.HasEdge("plan", "backlog") {
		t.Error("the declared recover entry must add plan→backlog")
	}
	if !spec.HasEdge("done", "in_progress") {
		t.Error("the first recover entry must still add its edge")
	}
	if got := spec.Start(); got != "backlog" {
		t.Errorf("Start() = %q with a recover edge into the entry state, want backlog", got)
	}
}

func TestRecoverSingleTableStillValid(t *testing.T) {
	done := `["*"]
obligations = ["raised", "readied", "coded", "closed"]
recover = { step = "in_progress", from = ["done"] }
`
	spec, err := ParseRoute(done, readinessSteps, "feature", nil)
	if err != nil {
		t.Fatalf("single-table recover must stay valid: %v", err)
	}
	if !spec.HasEdge("done", "in_progress") || spec.HasEdge("plan", "backlog") {
		t.Errorf("single-table recover edges wrong: %+v", spec.Transitions)
	}
}

func TestRecoverTypoIsStillAnUnknownKey(t *testing.T) {
	done := `["*"]
obligations = ["raised", "readied", "coded", "closed"]
recover = [ { step = "backlog", frm = ["plan"] } ]
`
	_, err := ParseRoute(done, readinessSteps, "feature", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("a typo inside a recover entry must be an unknown key, got %v", err)
	}
}

func TestReadinessKnobMisauthoringsAreRefusedByName(t *testing.T) {
	for _, tc := range []struct {
		name, step string
		want       []string
	}{
		{
			name: "propose without a performer",
			step: "[raised]\nstatus = \"backlog\"\nstart = true\n\n[readied]\nstatus = \"plan\"\npropose = true\nrequires = [\"raised\"]\n",
			want: []string{"step.toml", `step "readied"`, "propose needs"},
		},
		{
			name: "zero reject budget",
			step: "[raised]\nstatus = \"backlog\"\nstart = true\n\n[readied]\nstatus = \"plan\"\nreject_budget = 0\nrequires = [\"raised\"]\n",
			want: []string{`step "readied"`, "reject_budget must be >= 1"},
		},
		{
			name: "typo on the budget key",
			step: "[raised]\nstatus = \"backlog\"\nstart = true\n\n[readied]\nstatus = \"plan\"\nreject_budgt = 3\nrequires = [\"raised\"]\n",
			want: []string{"unknown key", "readied.reject_budgt"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSteps(tc.step)
			if err == nil {
				t.Fatal("want an error, got none")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must contain %q, got: %v", want, err)
				}
			}
		})
	}
}

func TestTwoFreezeStepsInOneRouteAreRefused(t *testing.T) {
	steps := strings.Replace(readinessSteps, "[readied]\nstatus = \"plan\"", "[readied]\nstatus = \"plan\"\nfreeze = true", 1)
	_, err := ParseRoute(readinessDone, steps, "feature", nil)
	if err == nil || !strings.Contains(err.Error(), "freeze") {
		t.Fatalf("two freeze steps must be refused, got %v", err)
	}
}

// A story can carry code of its own from the freeze step on, whichever agent
// performs it; the readiness step before the freeze never can (sty_87f407ef).
func TestCodeBearingStatesFollowTheFreezeStep(t *testing.T) {
	spec := readinessSpec(t)
	if got := spec.EditCapableStates(); len(got) != 0 {
		t.Fatalf("fixture has no executor step, EditCapableStates = %v", got)
	}
	if got := spec.CodeBearingStates(); !sameStrings(got, []string{"in_progress"}) {
		t.Errorf("CodeBearingStates = %v, want [in_progress] (coder at the freeze step, not the planner)", got)
	}

	// With no freeze step the executor rule is the whole answer.
	noFreeze := strings.Replace(readinessSteps, "freeze = true\n", "", 1)
	noFreeze = strings.Replace(noFreeze, "agent = \"coder\"", "agent = \"executor\"", 1)
	spec, err := ParseRoute(readinessDone, noFreeze, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if got, want := spec.CodeBearingStates(), spec.EditCapableStates(); !sameStrings(got, want) || !sameStrings(got, []string{"in_progress"}) {
		t.Errorf("no-freeze CodeBearingStates = %v, want EditCapableStates %v = [in_progress]", got, want)
	}
}

// The freeze bounds eligibility: an executor step placed before the freeze step
// is edit-capable but never code-bearing, because the definition is still open.
func TestExecutorBeforeFreezeIsNotCodeBearing(t *testing.T) {
	steps := strings.Replace(readinessSteps, "agent = \"planner\"", "agent = \"executor\"", 1)
	spec, err := ParseRoute(readinessDone, steps, "feature", nil)
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	if got := spec.EditCapableStates(); !sameStrings(got, []string{"plan"}) {
		t.Fatalf("fixture: EditCapableStates = %v, want [plan]", got)
	}
	if got := spec.CodeBearingStates(); !sameStrings(got, []string{"in_progress"}) {
		t.Errorf("CodeBearingStates = %v, want [in_progress]: the executor state before the freeze is not code-bearing", got)
	}
}

// AC2's route rule: editable strictly before the freeze step, by route order.
func TestDefinitionEditableFollowsTheFreezeStep(t *testing.T) {
	spec := readinessSpec(t)
	for status, want := range map[string]bool{
		"backlog":     true,
		"plan":        true, // readiness accepted; the definition-unchanged check covers this window
		"in_progress": false,
		"done":        false,
		"blocked":     false,
		"cancelled":   false,
		"nowhere":     false,
	} {
		got, ok := spec.DefinitionEditable(status)
		if !ok {
			t.Fatalf("%s: rule must resolve", status)
		}
		if got != want {
			t.Errorf("DefinitionEditable(%q) = %v, want %v", status, got, want)
		}
	}
}

// A route with no freeze step keeps the legacy rule: editable only in the entry
// state, so every existing route behaves exactly as before.
func TestDefinitionEditableLegacyWithoutFreeze(t *testing.T) {
	steps := strings.Replace(readinessSteps, "freeze = true\n", "", 1)
	spec, err := ParseRoute(readinessDone, steps, "feature", nil)
	if err != nil {
		t.Fatal(err)
	}
	for status, want := range map[string]bool{"backlog": true, "plan": false, "in_progress": false} {
		if got, ok := spec.DefinitionEditable(status); !ok || got != want {
			t.Errorf("legacy DefinitionEditable(%q) = %v/%v, want %v", status, got, ok, want)
		}
	}
}
