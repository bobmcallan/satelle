package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// showFixture stands up an isolated home + a data dir carrying agentsBody, and
// returns the app `workflow show` renders against. No store is needed: the view
// takes its skill resolver as a parameter.
func showFixture(t *testing.T, agentsBody string) *app.App {
	t.Helper()
	testutil.IsolateHome(t)
	dataDir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if agentsBody != "" {
		if err := os.WriteFile(filepath.Join(dataDir, config.AgentsConfigName), []byte(agentsBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &app.App{DataDir: dataDir}
}

func showDoc(fm string) docindex.Doc {
	return docindex.Doc{
		Kind: "workflows", Name: "w",
		Body: "---\nname: w\nscope: project\napplies_to: [\"*\"]\n" + fm + "---\n\n```dot\ndigraph w {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare]\n  backlog -> done [agent=reviewer, prompt=\"@skill:g\"]\n}\n```\n",
	}
}

func render(t *testing.T, a *app.App, doc docindex.Doc, resolves func(string) bool) string {
	t.Helper()
	buf := &bytes.Buffer{}
	if err := renderWorkflowShow(buf, a, doc, resolves); err != nil {
		t.Fatalf("renderWorkflowShow: %v", err)
	}
	return buf.String()
}

// TestWorkflowShowRendersHookAllocation is AC4's named proof: every field the
// acceptance criterion enumerates is asserted individually, so a dropped column
// fails by name rather than silently disappearing from the view.
func TestWorkflowShowRendersHookAllocation(t *testing.T) {
	a := showFixture(t, `
[reviewer]
role    = "reviewer"
command = "claude -p --disallowedTools Write,Edit --append-system-prompt {system} --model {model}"
tools   = "Read,Grep,Glob"

[strict-reviewer]
profile = "catalog-judge"
effort  = "high"
`)
	writeCatalog(t, `
[profiles.catalog-judge]
role    = "reviewer"
command = "claude -p --disallowedTools Write,Edit --append-system-prompt {system} --model {model}"
tools   = "Read,Grep,Glob"
model   = "opus"
`)
	doc := showDoc("hooks:\n  - operation: create_review\n    skill: strict-create-review\n    agent: strict-reviewer\n")
	out := render(t, a, doc, func(string) bool { return true })

	agentsFile := filepath.Join(a.DataDir, config.AgentsConfigName)
	for field, want := range map[string]string{
		"operation":          "HOOK create_review (verdict)",
		"skill":              "skill:      strict-create-review",
		"logical agent":      "agent:      agent=strict-reviewer (declared in hooks)",
		"resolved profile":   "binding:    local binding [strict-reviewer] over profile catalog-judge",
		"interface":          "interface:  command (backend isolated:claude)",
		"model":              "model:      opus",
		"effort":             "effort:     high",
		"permission ceiling": `ceiling:    read-only, tools="Read,Grep,Glob"`,
		"agents source file": agentsFile,
		"catalog source":     config.GlobalAgentsPath(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("%s missing — want %q in:\n%s", field, want, out)
		}
	}
	// Workflow identity + graph shape ride the same view.
	for _, want := range []string{"WORKFLOW w", "scope:      project", `applies_to: ["*"]`, "graph:      2 states"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestWorkflowShowRendersShorthandProvenance pins AC2's display half: the scalar
// form is shown as the DEFAULT allocation it is, and attributed to the shorthand
// — an operator can tell a choice from a default.
func TestWorkflowShowRendersShorthandProvenance(t *testing.T) {
	a := showFixture(t, "[reviewer]\nrole = \"reviewer\"\ncommand = \"claude -p --disallowedTools Write,Edit --append-system-prompt {system}\"\ntools = \"Read,Grep,Glob\"\n")
	out := render(t, a, showDoc("create_review: satelle-story-create-review\n"), func(string) bool { return true })

	for _, want := range []string{
		"agent:      agent=reviewer (default, from create_review shorthand)",
		"binding:    local binding [reviewer]",
		"skill:      satelle-story-create-review",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// A purely local binding must not claim a catalog source.
	if strings.Contains(out, config.GlobalAgentsPath()) {
		t.Errorf("a local binding must not list the machine-wide catalog as a source:\n%s", out)
	}
}

// TestWorkflowShowUnresolvedHookIsMarkedNotErrored pins the read-only posture:
// a hook naming a section with no binding, or a skill absent from the substrate,
// renders an explicit marker and still succeeds — diagnosing a misconfiguration
// is exactly what this command is for; refusing one is agent validate's job.
func TestWorkflowShowUnresolvedHookIsMarkedNotErrored(t *testing.T) {
	a := showFixture(t, "[reviewer]\nrole = \"reviewer\"\n")
	doc := showDoc("hooks:\n  - operation: create_review\n    skill: absent-skill\n    agent: nobody\n")
	out := render(t, a, doc, func(string) bool { return false })

	for _, want := range []string{
		"binding:    UNRESOLVED — agents.toml declares no [nobody]",
		"skill:      absent-skill  UNRESOLVED in the substrate",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// TestWorkflowShowSurfacesDeclarationProblems pins that a malformed or
// unrecognised declaration is visible here too, rather than only in validate.
func TestWorkflowShowSurfacesDeclarationProblems(t *testing.T) {
	a := showFixture(t, "[reviewer]\nrole = \"reviewer\"\n")
	out := render(t, a, showDoc("hooks:\n  - operation: close_review\n    skill: s\n"), func(string) bool { return true })
	if !strings.Contains(out, "operation:  UNKNOWN") {
		t.Errorf("an unknown operation must be marked:\n%s", out)
	}
	if !strings.Contains(out, "PROBLEM") || !strings.Contains(out, "close_review") {
		t.Errorf("the declaration problem must be printed:\n%s", out)
	}
}

// TestWorkflowShowNoHooksSaysSo keeps the common case legible: a workflow with
// no hook declares none, and the view says what that means.
func TestWorkflowShowNoHooksSaysSo(t *testing.T) {
	a := showFixture(t, "[reviewer]\nrole = \"reviewer\"\n")
	out := render(t, a, showDoc(""), func(string) bool { return true })
	if !strings.Contains(out, "(none declared — creation stays deterministic-only)") {
		t.Errorf("missing the no-hooks line:\n%s", out)
	}
}
