//go:build integration

package tests

import (
	"path/filepath"
	"testing"
)

// A lifecycle is a DERIVED ROUTE — done.md + step.md — and there is no DOT front
// end left to author one with (sty_d953c5d8). These helpers are what a black-box
// fixture uses in place of the retired `digraph` bodies: a category-specific lane
// is a `## <category>` SECTION of done.md rather than a second workflow file.

// routeFixture renders the two halves of a fixture route.
func routeFixture(done, step string) (doneDoc, stepDoc string) {
	head := func(name, what string) string {
		return "---\nname: " + name + "\ntype: workflow\nscope: project\ndescription: " + what + "\n---\n\n"
	}
	return head("done", "fixture declaration of done") + done,
		head("step", "fixture step catalogue") + step
}

// writeRouteFixture lands a fixture route in a repo's .satelle/workflows.
func writeRouteFixture(t *testing.T, repo, done, step string) {
	t.Helper()
	d, s := routeFixture(done, step)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"), d)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"), s)
}

// spineFixture is the shape most fixtures want: a wildcard lane of steps in
// order, each discharging an obligation named for it. steps are
// "name|agent|skill|reviewers(csv)|reviewer_agent"; the last is terminal.
func spineFixture(park, cancel string, gates string, steps ...string) (done, step string) {
	done = "## *\n- raised\n"
	step = "## backlog\nstart: true\nprovides: raised\n\n"
	prev := "raised"
	for i, spec := range steps {
		f := splitPipe(spec, 5)
		name, agent, skill, reviewers, ragent := f[0], f[1], f[2], f[3], f[4]
		ob := "ob-" + name
		done += "- " + ob + "\n"
		step += "## " + name + "\n"
		if agent != "" {
			step += "agent: " + agent + "\n"
		}
		if skill != "" {
			step += "skills: " + skill + "\n"
		}
		if reviewers != "" {
			step += "reviewers: " + reviewers + "\n"
		}
		if ragent != "" {
			step += "reviewer_agent: " + ragent + "\n"
		}
		if i == len(steps)-1 {
			step += "terminal: true\n"
		}
		step += "provides: " + ob + "\nrequires: " + prev + "\n\n"
		prev = ob
	}
	if park != "" {
		done += "park: " + park + "\n"
	}
	if cancel != "" {
		done += "cancel: " + cancel + "\n"
	}
	return routeFixture(done, step+gates)
}

// writeSpineFixture is writeRouteFixture over spineFixture.
func writeSpineFixture(t *testing.T, repo, park, cancel, gates string, steps ...string) {
	t.Helper()
	d, s := spineFixture(park, cancel, gates, steps...)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"), d)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"), s)
}

func splitPipe(s string, n int) []string {
	out := make([]string, 0, n)
	cur := ""
	for _, r := range s {
		if r == '|' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	for len(out) < n {
		out = append(out, "")
	}
	return out
}
