package config

import (
	"regexp"
	"strings"
	"testing"
)

// sty_b9d9a3df: the shipped container route carries the epic-branch lane. These
// resolve the embedded halves with no repo overlay, which is the route a repo
// with no workflow of its own runs.

func embeddedSkillBody(t *testing.T, name string) string {
	t.Helper()
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == name {
			return d.Body
		}
	}
	t.Fatalf("no embedded skill %q ships", name)
	return ""
}

// AC2: a `merged` step sits between readiness and the close on both container
// categories, waits for every child to close, and is performed by the shipped
// merge skill; the close is gated by the merged-check.
func TestEmbeddedContainerRouteMergesBeforeTheClose(t *testing.T) {
	specs := embeddedRouteSpecs()
	for _, category := range []string{"epic-parent", "parent"} {
		spec, ok := specs[category]
		if !ok {
			t.Fatalf("%s: the shipped route derives nothing", category)
		}
		var order []string
		for _, st := range spec.States {
			if st.Obligation != "" {
				order = append(order, st.Obligation)
			}
		}
		if got, want := strings.Join(order, ","), "raised,ready,merged,children-resolved"; got != want {
			t.Errorf("%s: obligations = %s, want %s", category, got, want)
		}
		merged, ok := spec.StateNamed("merging")
		if !ok {
			t.Fatalf("%s: no merging step", category)
		}
		if merged.Obligation != "merged" || merged.Agent != "executor" || merged.Skill != "satelle-epic-merge" {
			t.Errorf("%s: merging = {%s %s %s}, want merged/executor/satelle-epic-merge",
				category, merged.Obligation, merged.Agent, merged.Skill)
		}
		if got := spec.AfterChildren("merging"); got != "closed" {
			t.Errorf("%s: merging waits on %q, want closed", category, got)
		}
		if got := fingerprintOf(category); !strings.Contains(got, "admits=[satelle-epic-merged-check,") {
			t.Errorf("%s: the close is not gated by satelle-epic-merged-check:\n%s", category, got)
		}
	}
}

// ghWord matches the GitHub CLI as a word, not a substring of another word.
var ghWord = regexp.MustCompile(`\bgh\b`)

// AC2: the shipped merge rubric syncs trunk, publishes once through the binary's
// publish verb, and names no forge CLI except as an example of an override.
func TestEmbeddedEpicMergeSkillUsesTrunkMechanism(t *testing.T) {
	body := embeddedSkillBody(t, "satelle-epic-merge")
	for _, want := range []string{"satelle trunk sync", "satelle trunk publish --story", "--no-ff"} {
		if !strings.Contains(body, want) {
			t.Errorf("satelle-epic-merge does not name %q", want)
		}
	}
	for _, ln := range strings.Split(body, "\n") {
		if ghWord.MatchString(ln) && !strings.Contains(ln, "for example") {
			t.Errorf("a forge CLI appears outside an example: %q", ln)
		}
	}
	if !ghWord.MatchString(body) {
		t.Error("the override example should show a forge CLI, so a reader sees what an override replaces")
	}
}

// AC3: the shipped readiness step allows the `lane:` prefix, and the readiness
// skill tells the performer how to choose each child's lane.
func TestEmbeddedReadinessSetsLanes(t *testing.T) {
	for category, spec := range embeddedRouteSpecs() {
		if category != "epic-parent" && category != "parent" {
			continue
		}
		var allowed bool
		for _, st := range spec.States {
			if st.Obligation != "ready" {
				continue
			}
			for _, p := range st.ApplyChangeTags {
				allowed = allowed || p == "lane:"
			}
		}
		if !allowed {
			t.Errorf("%s: the readiness step's apply_changes does not allow the lane: prefix", category)
		}
	}
	body := embeddedSkillBody(t, "ready")
	for _, want := range []string{`"op":"add-tags"`, "lane:trunk", "Branch lane", "no `lane:` tag"} {
		if !strings.Contains(body, want) {
			t.Errorf("skill ready does not carry %q", want)
		}
	}
}

// sty_2b737ed5: the shipped ready rubric judges whether each child's intent is
// achievable in one pass, repairs without asking (chaining children with depends-on
// tags when they must run one at a time, never setting the user's sequential key),
// and rejects naming the child only when no repair can make it achievable.
func TestShippedReadySkillJudgesAchievabilityAndRepairsWithoutAsking(t *testing.T) {
	body := embeddedSkillBody(t, "ready")
	for _, want := range []string{
		"## Judge each child's intent",
		"achieved in\n**one pass**",
		"chain them with `depends-on:`",
		"**repair it yourself**",
		"Do not\n  ask the developer first",
		"naming the child",
		"you never set it",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skill ready does not carry %q", want)
		}
	}
}
