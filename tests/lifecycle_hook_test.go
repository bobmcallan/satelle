//go:build integration

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookRepo stands up a repo governed by a route whose declaration of done
// carries the given frontmatter lines, with the create gate on and a stub
// reviewer harness. It returns the repo path and a setter that rewrites the stub
// verdict, plus the path of the stub script so a second binding can point at it.
//
// A lifecycle hook is workflow FRONTMATTER, and a derived route's frontmatter
// lives on done.md — the half that says what this repo means by finished, which
// is where a create gate belongs.
func hookRepo(t *testing.T, wfFrontmatter string) (repo string, setVerdict func(decision, notes string), stub string) {
	t.Helper()
	repo = t.TempDir()
	mustRun(t, testBin, repo, "init")

	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"), "[review]\ngate_create = true\n")

	// The rubric the hook names. Its content does not matter — the stub decides.
	if err := os.MkdirAll(filepath.Join(repo, ".satelle", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "hook-create-review.md"), verdictRubric("hook-create-review"))

	if err := os.MkdirAll(filepath.Join(repo, ".satelle", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"),
		"---\nname: done\nscope: project\ntype: workflow\ndescription: Test declaration of done carrying a declared lifecycle hook.\n"+wfFrontmatter+
			"---\n\n## *\n- raised\n- coded\n- closed\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"),
		"---\nname: step\nscope: project\ntype: workflow\ndescription: Test step catalogue for the lifecycle-hook fixture route.\n---\n\n"+
			"## backlog\nstart: true\nprovides: raised\n\n"+
			"## in_progress\nagent: executor\nprovides: coded\nrequires: raised\n\n"+
			"## done\nterminal: true\nprovides: closed\nrequires: coded\n")

	stub = filepath.Join(repo, "verdict.sh")
	setVerdict = func(decision, notes string) {
		t.Helper()
		writeFile(t, stub, fmt.Sprintf("#!/bin/sh\necho '{\"decision\":\"%s\",\"notes\":\"%s\"}'\n", decision, notes))
		_ = os.Chmod(stub, 0o755)
	}
	setVerdict("accept", "")
	return repo, setVerdict, stub
}

// createFeature attempts a structurally-valid story create in the feature
// category.
func createFeature(t *testing.T, repo, title string) (string, error) {
	t.Helper()
	return run(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", title, "--body", "Render a widget on the dashboard", "--acceptance", "1. the widget renders")
}

// TestCreateHookDefaultShorthandRunsTheDefaultReviewer is AC6 case 1: the scalar
// shorthand keeps working and runs under [reviewer] — the compatibility floor.
func TestCreateHookDefaultShorthandRunsTheDefaultReviewer(t *testing.T) {
	repo, setVerdict, stub := hookRepo(t, "create_review: hook-create-review\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		fmt.Sprintf("[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \"%s {system} {tools} {model}\"\ntools = \"Read,Grep,Glob\"\n", stub))
	mustRun(t, testBin, repo, "reindex")

	// The allocation is now VISIBLE — the point of the story.
	show := mustRun(t, testBin, repo, "workflow", "show", "done")
	for _, want := range []string{
		"HOOK create_review (verdict)",
		"skill:      hook-create-review",
		"agent=reviewer (default, from create_review shorthand)",
		"binding:    local binding [reviewer]",
	} {
		if !strings.Contains(show, want) {
			t.Errorf("workflow show missing %q:\n%s", want, show)
		}
	}

	setVerdict("reject", "stub: shorthand reviewer says no")
	out, err := createFeature(t, repo, "Shorthand widget")
	if err == nil || !strings.Contains(out, "shorthand reviewer says no") {
		t.Fatalf("the shorthand-declared reviewer must gate creation:\n%s", out)
	}

	setVerdict("accept", "")
	if out, err := createFeature(t, repo, "Shorthand widget"); err != nil {
		t.Fatalf("accept should persist: %v\n%s", err, out)
	}
	if list := mustRun(t, testBin, repo, "story", "list"); !strings.Contains(list, "Shorthand widget") {
		t.Errorf("accepted draft should persist:\n%s", list)
	}
}

// TestCreateHookNamedLocalReviewer is AC6 case 2 and AC1's end-to-end proof: a
// hook naming a local binding runs THAT binding, not [reviewer]. The two stubs
// return different verdicts, so the story that gets created identifies which one
// actually ran.
func TestCreateHookNamedLocalReviewer(t *testing.T) {
	repo, _, defaultStub := hookRepo(t,
		"hooks:\n  - operation: create_review\n    skill: hook-create-review\n    agent: strict-reviewer\n")

	// [reviewer] always ACCEPTS; [strict-reviewer] always REJECTS. If the hook's
	// declared allocation were ignored and the old fallback used, creation would
	// succeed — so the reject IS the evidence.
	writeFile(t, defaultStub, "#!/bin/sh\necho '{\"decision\":\"accept\",\"notes\":\"default reviewer ran\"}'\n")
	_ = os.Chmod(defaultStub, 0o755)
	strictStub := filepath.Join(repo, "strict.sh")
	writeFile(t, strictStub, "#!/bin/sh\necho '{\"decision\":\"reject\",\"notes\":\"strict reviewer ran\"}'\n")
	_ = os.Chmod(strictStub, 0o755)

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"), fmt.Sprintf(
		"[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+
			"[reviewer]\nrole = \"reviewer\"\ncommand = \"%s {system} {tools} {model}\"\ntools = \"Read,Grep,Glob\"\n\n"+
			"[strict-reviewer]\nrole = \"reviewer\"\ncommand = \"%s {system} {tools} {model}\"\ntools = \"Read,Grep,Glob\"\nmodel = \"strict-model\"\n",
		defaultStub, strictStub))
	mustRun(t, testBin, repo, "reindex")

	show := mustRun(t, testBin, repo, "workflow", "show", "done")
	for _, want := range []string{
		"agent=strict-reviewer (declared in hooks)",
		"binding:    local binding [strict-reviewer]",
		"model:      strict-model",
	} {
		if !strings.Contains(show, want) {
			t.Errorf("workflow show missing %q:\n%s", want, show)
		}
	}
	validate := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(validate, "HOOK [done.md+step.md] hook:create_review") || !strings.Contains(validate, "agent=strict-reviewer") {
		t.Errorf("agent validate should surface the hook allocation:\n%s", validate)
	}

	out, err := createFeature(t, repo, "Named widget")
	if err == nil {
		t.Fatalf("the DECLARED strict-reviewer must run and reject; creation succeeded:\n%s", out)
	}
	if !strings.Contains(out, "strict reviewer ran") {
		t.Fatalf("the declared binding did not run — output:\n%s", out)
	}
	if strings.Contains(out, "default reviewer ran") {
		t.Fatal("the old [reviewer] fallback ran instead of the declared allocation")
	}
}

// TestCreateHookReferencedGlobalProfile is AC6 case 3: the hook's binding
// resolves its execution through a machine-wide profile (sty_c7dfeedf), and both
// the display and the actual invocation come from that profile.
func TestCreateHookReferencedGlobalProfile(t *testing.T) {
	repo, _, stub := hookRepo(t,
		"hooks:\n  - operation: create_review\n    skill: hook-create-review\n    agent: catalog-reviewer\n")
	writeCatalog(t, fmt.Sprintf(`
[profiles.judge]
role    = "reviewer"
command = "%s {system} {tools} {model}"
tools   = "Read,Grep,Glob"
model   = "profile-model"
`, stub))
	writeFile(t, stub, "#!/bin/sh\necho '{\"decision\":\"reject\",\"notes\":\"profile-supplied reviewer ran\"}'\n")
	_ = os.Chmod(stub, 0o755)

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		"[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+
			"[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p --disallowedTools Write,Edit --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n\n"+
			"[catalog-reviewer]\nprofile = \"judge\"\n")
	mustRun(t, testBin, repo, "reindex")

	show := mustRun(t, testBin, repo, "workflow", "show", "done")
	for _, want := range []string{
		"binding:    local binding [catalog-reviewer] over profile judge",
		"model:      profile-model",
		"agents.toml",
	} {
		if !strings.Contains(show, want) {
			t.Errorf("workflow show missing %q:\n%s", want, show)
		}
	}

	out, err := createFeature(t, repo, "Profile widget")
	if err == nil || !strings.Contains(out, "profile-supplied reviewer ran") {
		t.Fatalf("the profile-resolved binding must run the hook:\n%s", out)
	}
}

// TestCreateHookDeterministicCheckSkill is AC6 case 4: a hook whose skill
// carries a fenced check block is decided deterministically, with NO agent
// invoked — the stub records that it was never run.
func TestCreateHookDeterministicCheckSkill(t *testing.T) {
	repo, _, stub := hookRepo(t,
		"hooks:\n  - operation: create_review\n    skill: hook-check\n    agent: reviewer\n")
	marker := filepath.Join(repo, "agent-ran.marker")
	writeFile(t, stub, fmt.Sprintf("#!/bin/sh\ntouch %s\necho '{\"decision\":\"accept\"}'\n", marker))
	_ = os.Chmod(stub, 0o755)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		fmt.Sprintf("[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \"%s {system} {tools} {model}\"\ntools = \"Read,Grep,Glob\"\n", stub))

	// A deterministic check skill: the fenced command decides the verdict.
	writeFile(t, filepath.Join(repo, ".satelle", "skills", "hook-check.md"),
		"---\nname: hook-check\ntype: skill\ndescription: Deterministically refuse every draft so the check path is observable.\n---\n\n# Hook check\n\n"+
			"Return a JSON verdict with a decision field (accept|reject) and notes.\n\n```check\nexit 1\n```\n")
	mustRun(t, testBin, repo, "reindex")

	out, err := createFeature(t, repo, "Checked widget")
	if err == nil {
		t.Fatalf("a failing deterministic check must block creation:\n%s", out)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("a deterministic check skill must not invoke an agent")
	}
}

// TestCreateHookMissingAllocationIsRefusedByValidate is AC6 case 6. It pins the
// posture we ship: a hook naming a section with no binding is REFUSED up front
// by validate (non-zero exit, naming the section), while creation keeps today's
// structure-only degradation rather than becoming un-runnable. Validation is
// where a misconfiguration is caught; the engine does not grow a gate decision.
func TestCreateHookMissingAllocationIsRefusedByValidate(t *testing.T) {
	repo, _, stub := hookRepo(t,
		"hooks:\n  - operation: create_review\n    skill: hook-create-review\n    agent: nobody\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		fmt.Sprintf("[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \"%s {system} {tools} {model}\"\ntools = \"Read,Grep,Glob\"\n", stub))
	mustRun(t, testBin, repo, "reindex")

	out, err := run(t, testBin, repo, "agent", "validate")
	if err == nil {
		t.Fatalf("a hook naming an absent binding must fail validate:\n%s", out)
	}
	for _, want := range []string{"hook create_review", "no [nobody] binding"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate should name %q:\n%s", want, out)
		}
	}
	// show diagnoses rather than erroring — that is its read-only posture.
	showOut := mustRun(t, testBin, repo, "workflow", "show", "done")
	if !strings.Contains(showOut, "UNRESOLVED") {
		t.Errorf("workflow show should mark the unresolved allocation:\n%s", showOut)
	}
}

// TestCreateHookUnsafeAllocationsAreRefused is AC5 end-to-end: a verdict hook
// allocated to a non-reviewer role, or to an in-loop binding, is refused by
// validate before any story exists.
func TestCreateHookUnsafeAllocationsAreRefused(t *testing.T) {
	cases := map[string]struct{ binding, want string }{
		"non-reviewer role": {
			binding: "[worker]\nrole = \"agent\"\ncommand = \"claude -p --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n",
			want:    "want role=reviewer",
		},
		"in-loop verdict binding": {
			binding: "[worker]\nrole = \"reviewer\"\ncommand = \"in-loop\"\n",
			want:    "cannot produce an isolated verdict",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			repo, _, _ := hookRepo(t,
				"hooks:\n  - operation: create_review\n    skill: hook-create-review\n    agent: worker\n")
			writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
				"[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+
					"[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p --disallowedTools Write,Edit --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n\n"+c.binding)
			mustRun(t, testBin, repo, "reindex")

			out, err := run(t, testBin, repo, "agent", "validate")
			if err == nil {
				t.Fatalf("%s must fail validate:\n%s", name, out)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("validate should say %q:\n%s", c.want, out)
			}
		})
	}
}

// TestCreateHookAppliesAcrossCategories is what AC6 case 7 becomes.
//
// The original drove TWO workflow files with different applies_to and different
// hook allocations, proving the hook travelled with workflow SELECTION rather
// than being a global setting. A repo now has ONE lifecycle — a derived route —
// and its hooks ride on the single done.md, so a per-category hook allocation is
// no longer expressible and that leg retires with the DOT front end
// (sty_d953c5d8). Stated, not silently dropped: `hooks:` has no `for:` scoping,
// and adding one would be new behaviour this story does not carry.
//
// What remains true, and is what this test drives: the declared allocation is
// the one that runs, for EVERY category the route governs — including one the
// route never names, which its `## *` section covers.
func TestCreateHookAppliesAcrossCategories(t *testing.T) {
	repo, _, featureStub := hookRepo(t,
		"hooks:\n  - operation: create_review\n    skill: hook-create-review\n    agent: feature-reviewer\n")

	writeFile(t, featureStub, "#!/bin/sh\necho '{\"decision\":\"reject\",\"notes\":\"declared reviewer ran\"}'\n")
	_ = os.Chmod(featureStub, 0o755)

	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"), fmt.Sprintf(
		"[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+
			"[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p --disallowedTools Write,Edit --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n\n"+
			"[feature-reviewer]\nrole = \"reviewer\"\ncommand = \"%s {system} {tools} {model}\"\ntools = \"Read,Grep,Glob\"\n",
		featureStub))
	mustRun(t, testBin, repo, "reindex")

	out, _ := createFeature(t, repo, "Feature widget")
	if !strings.Contains(out, "declared reviewer ran") {
		t.Errorf("the feature category must run the declared allocation:\n%s", out)
	}

	out, _ = run(t, testBin, repo, "story", "create", "--category", "improvement",
		"--title", "Improve widget", "--body", "Make the widget faster", "--acceptance", "1. it is faster")
	if !strings.Contains(out, "declared reviewer ran") {
		t.Errorf("a category the route covers by `## *` must run the same declared allocation:\n%s", out)
	}
}

// verdictRubric is a minimal reviewer skill that satisfies the structural
// verdict contract satelle enforces on gate rubrics: it documents the JSON
// decision field and the notes field. The stub harness decides the actual
// verdict; this only has to be a well-formed rubric.
func verdictRubric(name string) string {
	return "---\nname: " + name + "\ntype: skill\n" +
		"description: Judge a draft story against its stated goal and acceptance criteria.\n---\n\n" +
		"# " + name + "\n\nJudge the draft. Return ONE JSON object:\n\n" +
		"```json\n{\"decision\": \"accept\", \"notes\": \"why\"}\n```\n\n" +
		"`decision` is `accept` or `reject`; `notes` carries the reasoning.\n"
}
