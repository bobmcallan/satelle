package config_test

import (
	"strings"
	"testing"
)

// sty_36b91fd1 AC1 + AC3: the shipped workflow advisor judges route shape over
// the governing routes, and says its findings are advice the orchestrator files
// as stories, never edits it makes to the workflow files.
func TestEmbeddedWorkflowAdvisorJudgesRouteShape(t *testing.T) {
	body := embeddedDefaultBody(t, "skills", "satelle-workflow-advisor")

	for _, want := range []string{
		// the check, over the governing route (shipped plus overlay)
		"Route shape fits the work's shape",
		"GOVERNING routes",
		"done.toml", "step.toml",
		// the three lanes
		"single-story lane", "epic-child lane", "epic-parent lane",
		// a release on the lane every child walks, and the optimisation it names
		"release obligation on the lane every epic child walks",
		"epic-branch lane", "epic/<id>", "satelle-epic-merge", "releases once",
		// an epic-parent lane with no readiness step
		"epic-parent lane with no readiness step", "waits_on_children",
		// each finding cites the file, the category table and the key
		"cites the workflow file, the category table and\n the obligation or step key",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the advisor rubric does not carry %q", want)
		}
	}

	for _, want := range []string{
		"Your findings are advice only",
		"`satelle story create`",
		"category `substrate`\nwhen the repo declares that category",
		"The advisor never edits workflow\nfiles itself",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the advisor rubric does not state the advisory clause %q", want)
		}
	}
}
