//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// namedAgentWF allocates the commit_push step to a named agent (agent=commit-agent)
// rather than the in-loop executor.
func namedAgentWF(name string) string {
	return "---\nname: " + name + "\ntype: workflow\nscope: project\napplies_to: [\"*\"]\ndescription: a wildcard workflow allocating commit_push to a named agent\n---\n" +
		"```dot\n" + `digraph w {
  backlog     [shape=Mdiamond]
  in_progress [agent=executor]
  commit_push [agent=commit-agent]
  done        [shape=Msquare, agent=reviewer]
  cancelled   [agent=reviewer]
  backlog -> in_progress -> commit_push -> done
  backlog -> cancelled
}` + "\n```\n"
}

// TestCommitAgentBindingInspectable proves the declarative commit-agent binding
// end-to-end (sty_b2222b8a): a node allocated to a named agent (agent=commit-agent),
// with the agent defined in .satelle/agents.toml, passes validate and is visible in
// workflow inspection. No new execution mechanism — the allocation is declared.
func TestCommitAgentBindingInspectable(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"),
		"[agents.commit-agent]\nharness = \"claude -p --allowedTools {tools}\"\ntools = \"Read,Bash(git:*)\"\n")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "named.md"), namedAgentWF("named"))
	mustRun(t, testBin, repo, "index")

	// validate stays green — a named-agent node is valid.
	if out, err := run(t, testBin, repo, "validate", "workflows", "named"); err != nil {
		t.Fatalf("validate should pass for a named-agent workflow:\n%s\n%v", out, err)
	}

	// Inspection shows commit_push allocated to the agent.
	out := mustRun(t, testBin, repo, "doc", "get", "workflows", "named")
	if !strings.Contains(out, "agent=commit-agent") {
		t.Errorf("workflow inspection should show commit_push bound to commit-agent:\n%s", out)
	}
}
