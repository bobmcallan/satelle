//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// A lifecycle is a DERIVED ROUTE — done.toml + step.toml — and there is no DOT
// front end left to author one with (sty_d953c5d8). These helpers are what a
// black-box fixture uses in place of the retired `digraph` bodies: a
// category-specific lane is a `[<category>]` TABLE of done.toml rather than a
// second workflow file.
//
// The EXTENSION is load-bearing (sty_81bb0dde): a `.md` half is refused by name
// as an unconverted repo, so a fixture written as markdown fails with a
// conversion error rather than the behaviour under test.

// routeFixture renders the two halves of a fixture route.
func routeFixture(done, step string) (doneDoc, stepDoc string) {
	head := func(name, what string) string {
		return "[meta]\nname = \"" + name + "\"\ntype = \"workflow\"\nscope = \"project\"\ndescription = \"" +
			what + "\"\n\n"
	}
	return head("done", "fixture declaration of done") + done,
		head("step", "fixture step catalogue") + step
}

// writeRouteFixture lands a fixture route in a repo's .satelle/workflows.
func writeRouteFixture(t *testing.T, repo, done, step string) {
	t.Helper()
	d, s := routeFixture(done, step)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), d)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), s)
}

// spineFixture is the shape most fixtures want: a wildcard lane of steps in
// order, each discharging an obligation named for it. steps are
// "name|agent|skill|reviewers(csv)|reviewer_agent"; the last is terminal.
func spineFixture(park, cancel string, gates string, steps ...string) (done, step string) {
	obligations := []string{`"raised"`}
	step = `[raised]
status = "backlog"
start = true

`
	prev := "raised"
	for i, spec := range steps {
		f := splitPipe(spec, 5)
		status, agent, skill, reviewers, ragent := f[0], f[1], f[2], f[3], f[4]
		ob := "ob-" + status
		obligations = append(obligations, `"`+ob+`"`)
		step += "[" + ob + "]\nstatus = \"" + status + "\"\n"
		if agent != "" {
			step += "agent = \"" + agent + "\"\n"
		}
		if skill != "" {
			step += "skills = " + tomlArray(skill) + "\n"
		}
		if reviewers != "" {
			step += "reviewers = " + tomlArray(reviewers) + "\n"
		}
		if ragent != "" {
			step += "reviewer_agent = \"" + ragent + "\"\n"
		}
		if i == len(steps)-1 {
			step += "terminal = true\n"
		}
		step += "requires = [\"" + prev + "\"]\n\n"
		prev = ob
	}
	done = "[\"*\"]\nobligations = [" + strings.Join(obligations, ", ") + "]\n"
	if park != "" {
		done += "park = " + roleRefTOML(park) + "\n"
	}
	if cancel != "" {
		done += "cancel = " + roleRefTOML(cancel) + "\n"
	}
	return routeFixture(done, step+gates)
}

// writeSpineFixture is writeRouteFixture over spineFixture.
func writeSpineFixture(t *testing.T, repo, park, cancel, gates string, steps ...string) {
	t.Helper()
	d, s := spineFixture(park, cancel, gates, steps...)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), d)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), s)
}

// tomlArray renders a CSV fixture field as a TOML array.
func tomlArray(csv string) string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, `"`+p+`"`)
		}
	}
	return "[" + strings.Join(out, ", ") + "]"
}

// roleRefTOML renders a fixture's "state @gate" shorthand as the inline table a
// route source declares a park/cancel role with.
func roleRefTOML(spec string) string {
	state, gate, _ := strings.Cut(spec, "@")
	fields := []string{`state = "` + strings.TrimSpace(state) + `"`}
	if gate = strings.TrimSpace(gate); gate != "" {
		fields = append(fields, `gate = "`+gate+`"`)
	}
	return "{ " + strings.Join(fields, ", ") + " }"
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
