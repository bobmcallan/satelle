package remotedef

import (
	"strings"
	"testing"
)

// twoLaneRemote is a scaffolded remote whose feature category has a "review"
// status the wildcard lane lacks, with the given segments in place of the
// scaffold's.
func twoLaneRemote(t *testing.T, segments string) string {
	t.Helper()
	dir := scaffolded(t)
	write(t, dir, "workflows/done.toml", `[meta]
name = "done"
type = "workflow"
scope = "project"
tags = ["type:workflow"]
description = "d"

["*"]
obligations = ["raised", "coded", "closed"]

[feature]
obligations = ["raised", "coded", "reviewed", "shipped"]
`)
	replaceIn(t, dir, "workflows/step.toml", "[closed]", `[reviewed]
status = "review"
agent = "worker"
skills = ["remote-work"]
requires = ["coded"]

[shipped]
status = "shipped"
terminal = true
requires = ["reviewed"]

[closed]`)
	replaceIn(t, dir, "remote.toml", "[[segment]]\nentry = \"backlog\"\nexit = \"done\"\n", segments)
	return dir
}

func TestSegmentCategories(t *testing.T) {
	for _, c := range []struct {
		name     string
		segments string
		wantKey  string // empty: validates clean
		wantText string
	}{
		{"scoped to a category with the status on its route",
			"[[segment]]\nentry = \"in_progress\"\nexit = \"review\"\ncategories = [\"feature\"]\n", "", ""},
		{"no categories means every category",
			"[[segment]]\nentry = \"in_progress\"\nexit = \"done\"\n", "", ""},
		{"a category with no section of its own is governed by the wildcard lane",
			"[[segment]]\nentry = \"in_progress\"\nexit = \"done\"\ncategories = [\"bug\"]\n", "", ""},
		{"an exit missing from a named category's route",
			"[[segment]]\nentry = \"in_progress\"\nexit = \"review\"\ncategories = [\"feature\", \"bug\"]\n",
			"segment[0].exit", `"review" is not a status of the bug route`},
		{"an entry that is no status of the workflow",
			"[[segment]]\nentry = \"nowhere\"\nexit = \"done\"\ncategories = [\"feature\"]\n",
			"segment[0].entry", `"nowhere" is not a status of this remote's own workflow`},
		{"an empty category",
			"[[segment]]\nentry = \"in_progress\"\nexit = \"done\"\ncategories = [\"\"]\n", "segment[0].categories[0]", "is empty"},
		{"overlapping segments at one entry",
			"[[segment]]\nentry = \"in_progress\"\nexit = \"done\"\n[[segment]]\nentry = \"in_progress\"\nexit = \"review\"\ncategories = [\"feature\"]\n",
			"segment[1].entry", "ambiguous"},
		{"the same entry for disjoint categories",
			"[[segment]]\nentry = \"in_progress\"\nexit = \"done\"\ncategories = [\"bug\"]\n[[segment]]\nentry = \"in_progress\"\nexit = \"review\"\ncategories = [\"feature\"]\n", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			fs := Validate(twoLaneRemote(t, c.segments))
			if c.wantKey == "" {
				if len(fs) != 0 {
					t.Fatalf("want a clean validation, got:\n%s", strings.Join(details(fs), "\n"))
				}
				return
			}
			if !hasFinding(fs, "remote.toml", c.wantKey) {
				t.Fatalf("no finding naming %s; got:\n%s", c.wantKey, strings.Join(details(fs), "\n"))
			}
			if !strings.Contains(strings.Join(details(fs), "\n"), c.wantText) {
				t.Errorf("findings lack %q:\n%s", c.wantText, strings.Join(details(fs), "\n"))
			}
		})
	}
}

func TestSegmentCategoryUnknownWithoutWildcard(t *testing.T) {
	dir := twoLaneRemote(t, "[[segment]]\nentry = \"in_progress\"\nexit = \"done\"\ncategories = [\"bug\"]\n")
	// Without a wildcard lane, "bug" is no category of this workflow.
	replaceIn(t, dir, "workflows/done.toml", `["*"]`, `[chore]`)
	fs := Validate(dir)
	if !hasFinding(fs, "remote.toml", "segment[0].categories[0]") ||
		!strings.Contains(strings.Join(details(fs), "\n"), `"bug" is not a category of this remote's own workflow (it declares chore, feature)`) {
		t.Fatalf("an undeclared category must be refused naming what is declared; got:\n%s", strings.Join(details(fs), "\n"))
	}
}

func TestStoryCommand(t *testing.T) {
	dir := scaffolded(t)
	replaceIn(t, dir, "remote.toml", "# story_command", "story_command")
	if fs := Validate(dir); len(fs) != 0 {
		t.Fatalf("the scaffold's story_command must validate, got:\n%s", strings.Join(details(fs), "\n"))
	}
	replaceIn(t, dir, "remote.toml", `story_command = "your-agent-cli {story}"`, `story_command = "your-agent-cli"`)
	fs := Validate(dir)
	if !hasFinding(fs, "remote.toml", "host.story_command") {
		t.Fatalf("a story_command without {story} must be refused; got:\n%s", strings.Join(details(fs), "\n"))
	}
}
