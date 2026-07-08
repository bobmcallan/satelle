package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func doc(name, body string) docindex.Doc {
	return docindex.Doc{Kind: "principles", Name: name, Body: body}
}

const sessionFM = "---\nname: c\ntags: [kind:principle, principles:session]\n---\n# Body\nresident text\n"
const plainFM = "---\nname: p\ntags: [kind:principle]\n---\n# Other\nnot resident\n"

func TestFrontmatterTags_inlineAndBlock(t *testing.T) {
	inline := frontmatterTags("---\ntags: [a, principles:session, c]\n---\nx")
	if len(inline) != 3 || inline[1] != "principles:session" {
		t.Fatalf("inline parse: %v", inline)
	}
	block := frontmatterTags("---\nname: x\ntags:\n  - a\n  - principles:session\nother: y\n---\nbody")
	if len(block) != 2 || block[1] != "principles:session" {
		t.Fatalf("block parse: %v", block)
	}
	if frontmatterTags("no frontmatter here") != nil {
		t.Fatalf("expected nil tags for no frontmatter")
	}
}

// selectAlwaysDocs is tag-driven: a principle carrying principles:session is the
// SESSION set (injected); one without the marker is on-demand (not injected).
// Residency is authored substrate, not a hardcoded name (epic:session-context).
func TestSelectAlwaysDocs_byResidencyTag(t *testing.T) {
	got := selectAlwaysDocs([]docindex.Doc{
		doc("satelle-agent-goals", sessionFM),    // tagged → session
		doc("satelle-agile-increments", plainFM), // untagged → on-demand
		doc("satelle-constitution", sessionFM),   // tagged → session
	})
	if len(got) != 2 {
		t.Fatalf("want the 2 session-tagged docs, got %d: %v", len(got), got)
	}
	for _, d := range got {
		if d.Name == "satelle-agile-increments" {
			t.Fatalf("on-demand (untagged) principle must not be injected: %v", got)
		}
	}
	// No session-tagged docs → nothing injected.
	if n := len(selectAlwaysDocs([]docindex.Doc{doc("p", plainFM)})); n != 0 {
		t.Fatalf("want 0 when no doc carries the session tag, got %d", n)
	}
}

func TestRenderAlwaysContent_bodyStrippedPlusInstruction(t *testing.T) {
	content, truncated := renderAlwaysContent("", []docindex.Doc{doc("c", sessionFM)}, alwaysContextCeiling)
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	if strings.Contains(content, "principles:session") {
		t.Fatalf("frontmatter leaked into injected content:\n%s", content)
	}
	if !strings.Contains(content, "resident text") {
		t.Fatalf("body missing from content:\n%s", content)
	}
	if !strings.Contains(content, alwaysIndexInstruction) {
		t.Fatalf("standing index instruction missing")
	}
}

func TestRenderAlwaysContent_emptySetStillTeachesIndex(t *testing.T) {
	content, _ := renderAlwaysContent("", nil, alwaysContextCeiling)
	if strings.Contains(content, "Always-resident") {
		t.Fatalf("no header expected with empty set:\n%s", content)
	}
	if !strings.Contains(content, alwaysIndexInstruction) {
		t.Fatalf("instruction must always be present")
	}
}

// The project constitution rides FIRST (order-zero), ahead of the principles.
func TestRenderAlwaysContent_constitutionFirst(t *testing.T) {
	content, truncated := renderAlwaysContent("This repo's constitution.", []docindex.Doc{doc("c", sessionFM)}, alwaysContextCeiling)
	if truncated {
		t.Fatalf("unexpected truncation")
	}
	if !strings.Contains(content, "# Project constitution") || !strings.Contains(content, "This repo's constitution.") {
		t.Fatalf("constitution not injected:\n%s", content)
	}
	if ci, pi := strings.Index(content, "This repo's constitution."), strings.Index(content, "resident text"); ci < 0 || pi < 0 || ci > pi {
		t.Fatalf("constitution must precede the principles:\n%s", content)
	}
}

func TestRenderAlwaysContent_ceilingTruncates(t *testing.T) {
	big := "---\ntags: [principles:session]\n---\n" + strings.Repeat("x", 200)
	docs := []docindex.Doc{doc("a", big), doc("b", big), doc("c", big)}
	content, truncated := renderAlwaysContent("", docs, 250) // fits one, not three
	if !truncated {
		t.Fatalf("expected truncation under a tight ceiling")
	}
	if strings.Count(content, "### ") > 1 {
		t.Fatalf("ceiling not enforced — too many docs injected:\n%s", content)
	}
}

func TestExecutorStates(t *testing.T) {
	body := "x\nstates:\n  - open\n  - {name: in_progress, agent: executor}\n  - blocked\n  - {name: deployed, agent: executor}\n  - done\ntransitions:\n  - {from: open, to: in_progress}\n"
	got := executorStates(body)
	if len(got) != 2 || got[0] != "in_progress" || got[1] != "deployed" {
		t.Fatalf("executorStates = %v, want [in_progress deployed]", got)
	}
}

// TestExecutorStatesAgentKey proves the inline-YAML executor-state parser reads the
// canonical `agent:` key and that the retired `actor:` key is NOT parsed
// (sty_7db2ed7d): a node authored with actor: executor is not treated as engaged.
func TestExecutorStatesAgentKey(t *testing.T) {
	body := "x\nstates:\n  - open\n  - {name: in_progress, agent: executor}\n  - {name: gate, agent: reviewer}\n  - done\ntransitions:\n  - {from: open, to: in_progress}\n"
	got := executorStates(body)
	if len(got) != 1 || got[0] != "in_progress" {
		t.Fatalf("executorStates(agent:) = %v, want [in_progress]", got)
	}
	// The retired actor: key must NOT register an executor state.
	legacy := "x\nstates:\n  - open\n  - {name: in_progress, actor: executor}\n  - done\ntransitions:\n  - {from: open, to: in_progress}\n"
	if got := executorStates(legacy); len(got) != 0 {
		t.Fatalf("retired actor: key must not register an executor state, got %v", got)
	}
}

func TestIsGitCommitOrPush(t *testing.T) {
	yes := []string{"git commit -m x", "cd /r && git push origin main"}
	no := []string{"ls", "git status", "git diff"}
	for _, c := range yes {
		if !isGitCommitOrPush(c) {
			t.Errorf("isGitCommitOrPush(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		// "echo git commit..." DOES contain 'git commit' — accept that v1 is a
		// substring check; only assert the clearly-non-commit cases.
		if c == "echo git commit is a phrase" {
			continue
		}
		if isGitCommitOrPush(c) {
			t.Errorf("isGitCommitOrPush(%q) = true, want false", c)
		}
	}
}

func TestBashCommandFromEvent(t *testing.T) {
	if got := bashCommandFromEvent([]byte(`{"tool_input":{"command":"git push"}}`)); got != "git push" {
		t.Errorf("bashCommandFromEvent = %q, want 'git push'", got)
	}
	if got := bashCommandFromEvent([]byte(`not json`)); got != "" {
		t.Errorf("bad event should yield empty command, got %q", got)
	}
}

func TestFilePathFromEvent(t *testing.T) {
	if got := filePathFromEvent([]byte(`{"tool_input":{"file_path":"/a/b.go"}}`)); got != "/a/b.go" {
		t.Errorf("file_path = %q, want /a/b.go", got)
	}
	if got := filePathFromEvent([]byte(`{"tool_input":{"notebook_path":"/a/n.ipynb"}}`)); got != "/a/n.ipynb" {
		t.Errorf("notebook_path = %q, want /a/n.ipynb", got)
	}
	if got := filePathFromEvent([]byte(`{}`)); got != "" {
		t.Errorf("absent path should yield empty, got %q", got)
	}
}

func TestWithinRoot(t *testing.T) {
	const root = "/home/u/repo"
	cases := []struct {
		target string
		want   bool // true = inside repo (gate applies); false = outside (allowed)
	}{
		{"/home/u/repo/internal/x.go", true},  // absolute, in-repo
		{"internal/x.go", true},               // relative, resolved under the repo cwd
		{"/tmp/claude/scratch/foo.sh", false}, // session scratchpad — outside
		{"/home/u/other/x.go", false},         // sibling dir — outside
		{"", true},                            // empty target — stay conservative
	}
	for _, c := range cases {
		if got := withinRoot(root, c.target); got != c.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", root, c.target, got, c.want)
		}
	}
}

// TestDataDirExemptionClassification proves the classification the edit gate's
// substrate exemption relies on (sty_103af456): a path under the data dir
// (.satelle/) resolves inside it (→ exempt from the story gate), while in-repo
// CODE outside it does not.
func TestDataDirExemptionClassification(t *testing.T) {
	const dataDir = "/home/u/repo/.satelle"
	cases := []struct {
		target string
		inData bool // true = under the data dir (edit exempt); false = code (gated)
	}{
		{"/home/u/repo/.satelle/skills/plan.md", true},
		{"/home/u/repo/.satelle/workflows/satelle-project-workflow.md", true},
		{"/home/u/repo/.satelle/agents.toml", true},
		{"/home/u/repo/internal/cli/cmd_hook.go", false}, // code — still gated
		{"/home/u/repo/README.md", false},                // repo-root non-substrate
	}
	for _, c := range cases {
		if got := withinRoot(dataDir, c.target); got != c.inData {
			t.Errorf("withinRoot(dataDir, %q) = %v, want %v", c.target, got, c.inData)
		}
	}
}

// TestEditExemptClassification proves the config-driven exempt list generalizes
// the data-dir exemption (sty_41416b76): a configured harness authoring dir
// (.claude/) is exempt while in-repo code stays gated, the data-dir leg still
// works, and a blank prefix must NOT silently exempt everything.
func TestEditExemptClassification(t *testing.T) {
	const dataDir = "/home/u/repo/.satelle"
	exemptRoots := []string{"/home/u/repo/.claude"}
	cases := []struct {
		name        string
		dataDir     string
		exemptRoots []string
		target      string
		want        bool
	}{
		{"configured harness dir exempt", dataDir, exemptRoots, "/home/u/repo/.claude/skills/foo/SKILL.md", true},
		{"data dir still exempt", dataDir, exemptRoots, "/home/u/repo/.satelle/skills/x.md", true},
		{"in-repo code stays gated", dataDir, exemptRoots, "/home/u/repo/internal/cli/app.go", false},
		{"unconfigured: code not exempt", dataDir, nil, "/home/u/repo/internal/cli/app.go", false},
		{"unconfigured: harness dir not exempt", dataDir, nil, "/home/u/repo/.claude/skills/x.md", false},
		{"blank prefix does not exempt everything", dataDir, []string{"  "}, "/home/u/repo/internal/cli/app.go", false},
	}
	for _, c := range cases {
		if got := editExempt(c.dataDir, c.exemptRoots, c.target); got != c.want {
			t.Errorf("%s: editExempt(%q) = %v, want %v", c.name, c.target, got, c.want)
		}
	}
}

// TestShouldWarnSubstrate pins the fail-open substrate nudge decision (sty_f5f351d1
// AC4): nudge ONLY for a data-dir edit with no engaged story. An engaged story, or
// an edit_exempt_paths opt-in dir (dataDirOnly=false), stays silent.
func TestShouldWarnSubstrate(t *testing.T) {
	cases := []struct {
		name        string
		dataDirOnly bool
		engaged     bool
		want        bool
	}{
		{"data dir, no story -> nudge", true, false, true},
		{"data dir, engaged -> silent", true, true, false},
		{"exempt-path dir, no story -> silent", false, false, false},
		{"exempt-path dir, engaged -> silent", false, true, false},
	}
	for _, c := range cases {
		if got := shouldWarnSubstrate(c.dataDirOnly, c.engaged); got != c.want {
			t.Errorf("%s: shouldWarnSubstrate(%v, %v) = %v, want %v", c.name, c.dataDirOnly, c.engaged, got, c.want)
		}
	}
}

func TestExecutorStatesDOT(t *testing.T) {
	body := `---
name: x
---
` + "```dot" + `
digraph w {
  in_progress [agent=executor]
  committed   [agent=reviewer, prompt="@skill:r"]
  in_progress -> committed -> done
}
` + "```" + `
`
	got := executorStates(body)
	if len(got) != 1 || got[0] != "in_progress" {
		t.Fatalf("executorStates = %v, want [in_progress]", got)
	}
}

// TestExecutorStatesNamedAgent proves a node allocated to a NAMED agent (not
// executor/reviewer) counts as an engaged performing state (sty_b2222b8a), so a
// commit at that step is tracked, while reviewer nodes do not count.
func TestExecutorStatesNamedAgent(t *testing.T) {
	body := `---
name: x
---
` + "```dot" + `
digraph w {
  in_progress [agent=executor]
  commit_push [agent=commit-agent, prompt="@skill:commit-push"]
  committed   [agent=reviewer, prompt="@skill:r"]
  in_progress -> commit_push -> committed -> done
}
` + "```" + `
`
	got := executorStates(body)
	has := func(n string) bool {
		for _, s := range got {
			if s == n {
				return true
			}
		}
		return false
	}
	if !has("in_progress") || !has("commit_push") {
		t.Fatalf("named-agent commit_push should be engaged: %v", got)
	}
	if has("committed") {
		t.Errorf("a reviewer node must not count as engaged: %v", got)
	}
}

// TestExecutorStatesCoderNodeEngaged: a repo that opts in_progress into a
// dispatched coder (agent=coder) still has in_progress as an ENGAGED performing
// state (sty_f5bd176f) — so the edit gate allows the coder's edits while the story
// is in_progress, and would NOT treat the non-performing backlog as engaged.
func TestExecutorStatesCoderNodeEngaged(t *testing.T) {
	body := `---
name: x
---
` + "```dot" + `
digraph w {
  backlog     [shape=Mdiamond]
  plan        [agent=planner, prompt="@skill:plan"]
  in_progress [agent=coder, prompt="@skill:code"]
  done        [shape=Msquare]
  backlog -> plan -> in_progress -> done
}
` + "```" + `
`
	got := executorStates(body)
	engaged := map[string]bool{}
	for _, s := range got {
		engaged[s] = true
	}
	if !engaged["plan"] || !engaged["in_progress"] {
		t.Fatalf("plan and coder in_progress should be engaged: %v", got)
	}
	if engaged["backlog"] || engaged["done"] {
		t.Errorf("terminal markers must not be engaged: %v", got)
	}
}

// wfDoc builds a workflow Doc with the given name, applies_to frontmatter value
// (e.g. `["*"]`), and DOT node/edge lines.
func wfDoc(name, appliesTo, dot string) docindex.Doc {
	body := "---\nname: " + name + "\napplies_to: " + appliesTo + "\n---\n" +
		"```dot\ndigraph w {\n" + dot + "\n}\n```\n"
	return docindex.Doc{Kind: "workflows", Name: name, Body: body}
}

func TestAnyEngagedCountsTasks(t *testing.T) {
	// One wildcard workflow governs both the story and the task; commit_push is a
	// performing named-agent node.
	wfs := []docindex.Doc{wfDoc("wf", `["*"]`, `
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  commit_push [agent=commit-agent, prompt="@skill:commit-push"]
  done        [shape=Msquare]
  backlog -> in_progress -> commit_push -> done`)}
	// A task in a performing state counts as engaged, exactly like a story.
	if !anyEngaged([]workitem.Item{
		{Kind: workitem.KindTask, Status: "commit_push"},
		{Kind: workitem.KindStory, Status: "backlog"},
	}, wfs) {
		t.Error("a task in a performing state should count as engaged")
	}
	// Nothing engaged when no item is in a performing state.
	if anyEngaged([]workitem.Item{
		{Kind: workitem.KindTask, Status: "backlog"},
		{Kind: workitem.KindStory, Status: "done"},
	}, wfs) {
		t.Error("no item in a performing state should not count as engaged")
	}
}

// TestAnyEngagedPerStoryWorkflow: engagement is judged against the story's OWN
// governing workflow, not one global "primary" workflow (sty_f5bd176f). A feature
// story in a feature-workflow's performing `plan` state is engaged even though the
// wildcard workflow has no `plan` node — the exact gap that blocked a dispatched
// coder reached from `plan`.
func TestAnyEngagedPerStoryWorkflow(t *testing.T) {
	wfs := []docindex.Doc{
		wfDoc("wild", `["*"]`, `
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  done        [shape=Msquare]
  backlog -> in_progress -> done`),
		wfDoc("feat", `["feature"]`, `
  backlog     [shape=Mdiamond]
  plan        [agent=executor]
  in_progress [agent=coder, prompt="@skill:code"]
  done        [shape=Msquare]
  backlog -> plan -> in_progress -> done`),
	}
	// A feature story in plan is engaged via the feature workflow (plan performs).
	if !anyEngaged([]workitem.Item{{Kind: workitem.KindStory, Category: "feature", Status: "plan"}}, wfs) {
		t.Error("a feature story in its workflow's performing plan state should be engaged")
	}
	// The same status under the wildcard workflow (no plan node) is NOT engaged —
	// engagement follows the governing workflow, not a global state list.
	if anyEngaged([]workitem.Item{{Kind: workitem.KindStory, Category: "chore", Status: "plan"}}, wfs) {
		t.Error("a chore story in plan is governed by the wildcard workflow (no plan state) — not engaged")
	}
}
