//go:build integration && operatorconfig

package tests

import (
	"regexp"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/wfroute"
)

// sty_4cf2c585: every gated step of this repo's route declares a reject_budget, so
// a gate rejection is a bounded loop rather than operator work, and
// `satelle workflow show` reports exactly the budget the catalogue declares.
// It pins the AUTHORED route under .satelle/workflows, which a clean checkout
// does not carry, so it runs under the operatorconfig opt-in (make operator-check).
//
// "Gated" is the route's own definition, not a list kept here: a step the route
// view reports entry reviewers for — the edge's declared reviewers plus the
// always-on scoped layer. The start step has no entry edge and is not gated.
func TestOperatorEveryGatedStepDeclaresRejectBudgetAndShowAgrees(t *testing.T) {
	doneBody, stepBody := repoRouteSource(t)
	lists, err := wfdot.ParseDone(doneBody)
	if err != nil {
		t.Fatalf("ParseDone: %v", err)
	}
	cat, err := wfdot.ParseSteps(stepBody)
	if err != nil {
		t.Fatalf("ParseSteps: %v", err)
	}
	declared := map[string]int{} // obligation → budget the catalogue declares
	for _, st := range cat.Steps {
		declared[st.Provides] = st.RejectBudget
	}

	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	seedRepoRouteSource(t, repo)

	for _, l := range lists {
		spec, err := wfdot.BuildRoute(l, cat, nil)
		if err != nil {
			t.Fatalf("category %q: BuildRoute: %v", l.Category, err)
		}
		if problems := wfdot.Validate(spec); len(problems) != 0 {
			t.Errorf("category %q: route does not validate: %v", l.Category, problems)
		}
		shown := shownRejectBudgets(mustRun(t, testBin, repo, "workflow", "show", l.Category, "--category"))
		for _, step := range wfroute.Build(spec, "derived", nil, nil, nil).Steps {
			if got := shown[step.Obligation]; got != declared[step.Obligation] {
				t.Errorf("category %q step %q: `workflow show` reports reject budget %d, step.toml declares %d",
					l.Category, step.Obligation, got, declared[step.Obligation])
			}
			if len(step.Reviewers) > 0 && declared[step.Obligation] < 1 {
				t.Errorf("category %q: gated step %q (status %s, %d entry reviewers) declares no reject_budget",
					l.Category, step.Obligation, step.Status, len(step.Reviewers))
			}
		}
	}
}

var (
	dischargesLine = regexp.MustCompile(`^\s+discharges: (\S+)`)
	rejectBudgetRe = regexp.MustCompile(`^\s+reject budget: (\d+)`)
)

// shownRejectBudgets reads obligation → budget out of a `workflow show <category>
// --category` rendering; a step that prints no budget line is absent (0).
func shownRejectBudgets(out string) map[string]int {
	budgets := map[string]int{}
	obligation := ""
	for _, ln := range strings.Split(out, "\n") {
		if m := dischargesLine.FindStringSubmatch(ln); m != nil {
			obligation = m[1]
		}
		if m := rejectBudgetRe.FindStringSubmatch(ln); m != nil && obligation != "" {
			n := 0
			for _, c := range m[1] {
				n = n*10 + int(c-'0')
			}
			budgets[obligation] = n
		}
	}
	return budgets
}
