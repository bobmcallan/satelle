//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestCommitAgentBindingInspectable proves the declarative commit-agent binding
// end-to-end (sty_b2222b8a): a step allocated to a named agent (agent: commit-agent),
// with the agent defined in .satelle/agents.toml (flat [commit-agent] form),
// passes validate and is visible in workflow inspection. Nested [agents.name]
// is no longer a live dual-read (breaking surface — MigrateAgents flattens on init).
func TestCommitAgentBindingInspectable(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"),
		"[commit-agent]\ncommand = \"claude -p --allowedTools {tools}\"\ntools = \"Read,Bash(git:*)\"\n")
	writeSpineFixture(t, repo, "", "", "",
		"in_progress|executor|||",
		"commit_push|commit-agent|||",
		"done||||")
	mustRun(t, testBin, repo, "reindex")

	if out, err := run(t, testBin, repo, "workflow", "validate"); err != nil {
		t.Fatalf("validate should pass for a named-agent route:\n%s\n%v", out, err)
	}
	// `doc get` emits JSON, so the body's own quotes arrive escaped — this is the
	// route source as an operator would read it back off the index.
	out := mustRun(t, testBin, repo, "doc", "get", "workflows", "step")
	if !strings.Contains(out, `agent = \"commit-agent\"`) {
		t.Errorf("workflow inspection should show commit_push bound to commit-agent:\n%s", out)
	}
}
