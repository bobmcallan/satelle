package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// sty_73224b86: the derived route view reports a step's answer_progress rule (and
// only the steps that enable it), and validate refuses a malformed table or one
// on a step that does not propose.

func TestWorkflowShowRouteReportsAnswerProgress(t *testing.T) {
	rs := routeSourceFixture()
	rs.Step = strings.Replace(rs.Step, "requires = [\"raised\"]\n\n[released]",
		"requires = [\"raised\"]\npropose = true\n\n[coded.answer_progress]\nenabled = true\nmin_progress = 2\n\n[released]", 1)
	var b strings.Builder
	if err := renderWorkflowRoute(&b, rs, "feature", nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	var lines []string
	for _, ln := range strings.Split(b.String(), "\n") {
		if strings.Contains(ln, "answer progress:") {
			lines = append(lines, strings.TrimSpace(ln))
		}
	}
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "answer progress: on, min_progress 2 ") || !strings.Contains(lines[0], "refused as unanswered findings before its gates run") {
		t.Fatalf("want exactly one `answer progress: on, min_progress 2` line, got %q in:\n%s", lines, b.String())
	}
	if out := renderRoute(t, "feature", nil); strings.Contains(out, "answer progress") {
		t.Errorf("a route declaring no rule must render none:\n%s", out)
	}
}

func answerProgressRouteStep(propose, table string) string {
	return `[raised]
status = "backlog"
start = true

[ready]
status = "plan"
agent = "planner"
skills = ["plan"]
` + propose + `requires = ["raised"]
` + table + `
[coded]
status = "in_progress"
agent = "executor"
requires = ["ready"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`
}

func TestValidateRefusesAMalformedAnswerProgressTable(t *testing.T) {
	cases := []struct {
		name, propose, table string
		want                 string // empty: validate passes
	}{
		{"enabled on a propose step", "propose = true\n", "[ready.answer_progress]\nenabled = true\nmin_progress = 2\n", ""},
		{"absent", "propose = true\n", "", ""},
		{"min_progress 0", "propose = true\n", "[ready.answer_progress]\nenabled = true\nmin_progress = 0\n", "min_progress must be >= 1"},
		{"missing enabled", "propose = true\n", "[ready.answer_progress]\nmin_progress = 1\n", "needs enabled"},
		{"non-propose step", "", "[ready.answer_progress]\nenabled = true\n", "answer_progress requires propose = true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := tempRepo(t)
			t.Chdir(repo)
			writeRoute(t, filepath.Join(repo, ".satelle", "workflows"), scheduleRouteDone, answerProgressRouteStep(tc.propose, tc.table))
			out, err := runRootIn(t, "", "validate")
			if tc.want == "" {
				if strings.Contains(out, "answer_progress") {
					t.Fatalf("a well-formed table must not be named in a failure:\n%s", out)
				}
				return
			}
			if err == nil || !strings.Contains(out, "FAIL") || !strings.Contains(out, tc.want) {
				t.Fatalf("validate must FAIL naming %q (err=%v):\n%s", tc.want, err, out)
			}
		})
	}
}
