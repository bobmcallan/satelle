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
	content, truncated := renderAlwaysContent([]docindex.Doc{doc("c", sessionFM)}, alwaysContextCeiling)
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
	content, _ := renderAlwaysContent(nil, alwaysContextCeiling)
	if strings.Contains(content, "Always-resident") {
		t.Fatalf("no header expected with empty set:\n%s", content)
	}
	if !strings.Contains(content, alwaysIndexInstruction) {
		t.Fatalf("instruction must always be present")
	}
}

func TestRenderAlwaysContent_ceilingTruncates(t *testing.T) {
	big := "---\ntags: [principles:session]\n---\n" + strings.Repeat("x", 200)
	docs := []docindex.Doc{doc("a", big), doc("b", big), doc("c", big)}
	content, truncated := renderAlwaysContent(docs, 250) // fits one, not three
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

func TestAnyEngagedCountsTasks(t *testing.T) {
	engaged := map[string]bool{"in_progress": true, "commit_push": true}
	// A task in an executor state counts as engaged, exactly like a story.
	if !anyEngaged([]workitem.Item{
		{Kind: workitem.KindTask, Status: "commit_push"},
		{Kind: workitem.KindStory, Status: "backlog"},
	}, engaged) {
		t.Error("a task in an executor state should count as engaged")
	}
	// Nothing engaged when no item is in an executor state.
	if anyEngaged([]workitem.Item{
		{Kind: workitem.KindTask, Status: "backlog"},
		{Kind: workitem.KindStory, Status: "done"},
	}, engaged) {
		t.Error("no item in an executor state should not count as engaged")
	}
}
