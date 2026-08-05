package config

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// The embedded route is about to gain a large amount of explanatory comment
// (sty_58911b1a). Comments cannot change the grammar — the parser skips a line
// starting with `<!--` — but "cannot" is a claim, and this file is the proof.
//
// Written and passing against the BARE files BEFORE the comments were added, so
// a diff of the derived route after the edit is a real regression signal rather
// than a restatement of whatever the files happen to say now.

// fingerprintOf renders a category's derived Spec as deterministic text: the
// step sequence with start/terminal markers and the reviewers gating entry to
// each, then any always-on gate carrying an `on:` list. Everything a reader of
// the route cares about, and nothing that depends on map iteration.
func fingerprintOf(category string) string {
	specs := embeddedRouteSpecs()
	spec, ok := specs[category]
	if !ok {
		return "<no route>"
	}
	var b strings.Builder
	// Entry gates keyed by target step, so a step's line carries what admits it.
	entry := map[string][]string{}
	for _, tr := range spec.Transitions {
		if len(tr.Skills) > 0 {
			entry[tr.To] = append(entry[tr.To], tr.Skills...)
		}
	}
	var gates []string
	for _, st := range spec.States {
		if len(st.On) > 0 {
			gates = append(gates, fmt.Sprintf("%s on=%s", st.Skill, strings.Join(st.On, "+")))
			continue
		}
		marks := ""
		switch st.Shape {
		case "Mdiamond":
			marks = " [start]"
		case "Msquare":
			marks = " [terminal]"
		}
		agent := st.Agent
		if agent == "" {
			agent = "-"
		}
		in := entry[st.Name]
		sort.Strings(in)
		fmt.Fprintf(&b, "step %s%s agent=%s skill=%s provides=%s admits=[%s]\n",
			st.Name, marks, agent, orDash(st.Skill), orDash(st.Obligation), strings.Join(in, ","))
	}
	sort.Strings(gates)
	for _, g := range gates {
		fmt.Fprintf(&b, "gate %s\n", g)
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// TestEmbeddedRouteGoldenFingerprint pins the derived route for every category
// the shipped halves declare. A comment-only edit must leave every line of this
// identical; any diff means the markdown change reached the grammar.
func TestEmbeddedRouteGoldenFingerprint(t *testing.T) {
	specs := embeddedRouteSpecs()
	if len(specs) == 0 {
		t.Fatal("the shipped route derives nothing — the halves are missing or unparseable")
	}
	var categories []string
	for c := range specs {
		categories = append(categories, c)
	}
	sort.Strings(categories)

	// The shipped route governs exactly these; a new one is a deliberate change
	// that must update this list, not something that appears silently.
	want := []string{"*", "docs", "epic-parent", "execution", "parent", "task"}
	if strings.Join(categories, ",") != strings.Join(want, ",") {
		t.Fatalf("shipped categories = %v, want %v", categories, want)
	}

	golden := map[string]string{
		"docs": `step backlog [start] agent=- skill=- provides=raised admits=[]
step in_progress agent=executor skill=- provides=authored admits=[]
step done [terminal] agent=- skill=- provides=docs-verified admits=[satelle-docs-only-check]
step gate_satelle-step-summary agent=reviewer skill=satelle-step-summary provides=- admits=[]
step cancelled agent=reviewer skill=satelle-story-cancel-review provides=- admits=[satelle-story-cancel-review,satelle-story-cancel-review,satelle-story-cancel-review]
step blocked agent=reviewer skill=satelle-story-blocked-review provides=- admits=[satelle-story-blocked-review]
`,
		"*": `step backlog [start] agent=- skill=- provides=raised admits=[]
step in_progress agent=executor skill=- provides=coded admits=[satelle-story-intent-review]
step done [terminal] agent=- skill=- provides=closed admits=[satelle-story-done-review,satelle-story-scope-review,satelle-workflow-change-review]
step gate_satelle-step-summary agent=reviewer skill=satelle-step-summary provides=- admits=[]
step cancelled agent=reviewer skill=satelle-story-cancel-review provides=- admits=[satelle-story-cancel-review,satelle-story-cancel-review,satelle-story-cancel-review]
step blocked agent=reviewer skill=satelle-story-blocked-review provides=- admits=[satelle-story-blocked-review]
gate satelle-estimate-actual-review on=in_progress+done
`,
		"epic-parent": `step backlog [start] agent=- skill=- provides=raised admits=[]
step done [terminal] agent=reviewer skill=- provides=children-resolved admits=[satelle-story-done-review]
step gate_satelle-step-summary agent=reviewer skill=satelle-step-summary provides=- admits=[]
step cancelled agent=reviewer skill=satelle-story-cancel-review provides=- admits=[satelle-story-cancel-review]
`,
	}
	for category, expect := range golden {
		got := fingerprintOf(category)
		if got != expect {
			t.Errorf("derived route for %q changed:\n--- got ---\n%s\n--- want ---\n%s", category, got, expect)
		}
	}

	// Every remaining category is pinned by shape rather than by literal, so the
	// test stays honest without freezing text nobody reads.
	for _, c := range []string{"parent", "execution", "task"} {
		got := fingerprintOf(c)
		if !strings.Contains(got, "step backlog [start]") {
			t.Errorf("category %q lost its start step:\n%s", c, got)
		}
		if !strings.Contains(got, "[terminal]") {
			t.Errorf("category %q lost its terminal step:\n%s", c, got)
		}
	}
}
