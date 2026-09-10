package wfdot

import (
	"reflect"
	"strings"
	"testing"
)

// The step key half of sty_8e0b29a0 AC1: `rework = { consult, rounds }` parses
// onto the Step, its three mis-authorings are refused by name, and a step
// WITHOUT the key parses to exactly the Step it did before the key existed.

const reworkDone = "[feature]\nobligations = [\"raised\", \"coded\"]\n"

func reworkStepBody(reworkLine string) string {
	return `[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "coder"
skills = ["code"]
requires = ["raised"]
` + reworkLine
}

func stepProviding(t *testing.T, cat Catalogue, provides string) Step {
	t.Helper()
	for _, st := range cat.Steps {
		if st.Provides == provides {
			return st
		}
	}
	t.Fatalf("no step provides %q", provides)
	return Step{}
}

func TestReworkKeyParsesOntoStep(t *testing.T) {
	cat, err := ParseSteps(reworkStepBody(`rework = { consult = "reviewer", rounds = 3 }` + "\n"))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "coded")
	if st.ReworkConsult != "reviewer" {
		t.Errorf("ReworkConsult = %q, want reviewer", st.ReworkConsult)
	}
	if st.ReworkRounds != 3 {
		t.Errorf("ReworkRounds = %d, want 3", st.ReworkRounds)
	}
	// The whole route must still build: the key is an annotation, not topology.
	if _, err := ParseRoute(reworkDone, reworkStepBody(`rework = { consult = "reviewer", rounds = 3 }`+"\n"), "feature", nil); err != nil {
		t.Fatalf("ParseRoute with rework: %v", err)
	}
}

// TestReworkAbsentIsByteIdentical is the AC1 no-op guarantee: a step with no
// rework key parses to a Step deep-equal to the one the same body produces
// today, with both new fields at their zero value. If a default were ever
// synthesised here, this is the test that catches it.
func TestReworkAbsentIsByteIdentical(t *testing.T) {
	cat, err := ParseSteps(reworkStepBody(""))
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	st := stepProviding(t, cat, "coded")
	want := Step{
		Name: "in_progress", Provides: "coded", Agent: "coder",
		Skills: []string{"code"}, Requires: []string{"raised"},
	}
	if !reflect.DeepEqual(st, want) {
		t.Errorf("step without rework =\n %+v\nwant\n %+v", st, want)
	}
}

func TestReworkMisauthoringsAreRefusedByName(t *testing.T) {
	for _, tc := range []struct {
		name, step string
		want       []string
	}{
		{
			name: "no consult",
			step: reworkStepBody(`rework = { rounds = 2 }` + "\n"),
			want: []string{"step.toml", `step "coded"`, "consult"},
		},
		{
			// A zero budget is a loop that never runs — indistinguishable in
			// behaviour from no key, and therefore a lie about the process.
			name: "zero rounds",
			step: reworkStepBody(`rework = { consult = "reviewer", rounds = 0 }` + "\n"),
			want: []string{"step.toml", `step "coded"`, "rounds must be > 0"},
		},
		{
			name: "negative rounds",
			step: reworkStepBody(`rework = { consult = "reviewer", rounds = -1 }` + "\n"),
			want: []string{"rounds must be > 0"},
		},
		{
			name: "no performer to code with",
			step: `[raised]
status = "backlog"
start = true

[closed]
status = "done"
terminal = true
requires = ["raised"]
rework = { consult = "reviewer", rounds = 2 }
`,
			want: []string{"step.toml", `step "closed"`, "allocate a performer"},
		},
		{
			// Strictness is the point: a typo inside the sub-table must not read
			// as "no rework".
			name: "typo inside the sub-table",
			step: reworkStepBody(`rework = { consult = "reviewer", round = 2 }` + "\n"),
			want: []string{"step.toml", "unknown key", "coded.rework.round"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSteps(tc.step)
			if err == nil {
				t.Fatalf("want an error, got none")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must contain %q, got: %v", want, err)
				}
			}
		})
	}
}
