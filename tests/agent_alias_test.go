//go:build integration

package tests

import (
	"path/filepath"
	"strings"
	"testing"
)

// agentAliasWF is a wildcard workflow whose in_progress executor node is declared
// with the canonical agent= keyword (not the legacy actor=) and names a skill that
// does not resolve. The deterministic workflow structure check only collects an
// executor-path skill from a node it recognises AS an executor — so it can only
// report the unresolved skill if agent=executor was parsed as the performer.
func agentAliasWF(name string) string {
	return "---\nname: " + name + "\ntype: workflow\napplies_to: [\"*\"]\ndescription: a test wildcard workflow using the agent keyword\n---\n" +
		"```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor, prompt="@skill:agent-alias-missing-skill"]
  done [shape=Msquare, agent=reviewer]
  cancelled [agent=reviewer]
  backlog -> in_progress
  in_progress -> done
  backlog -> cancelled
}` + "\n```\n"
}

// TestAgentKeywordParsesEndToEnd proves the agent= back-compat parse end-to-end
// through the real binary (sty_536f9960): a workflow authored with agent=executor
// is parsed as having an executor node, so `satelle validate` reports its
// unresolved executor-path skill — which it could only do if agent= was honoured
// as the performer keyword.
func TestAgentKeywordParsesEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agent-kw.md"), agentAliasWF("agent-kw"))
	mustRun(t, testBin, repo, "index")

	out, err := run(t, testBin, repo, "validate", "workflows")
	if err == nil {
		t.Fatalf("validate should fail: the agent=executor node names an unresolved skill:\n%s", out)
	}
	if !strings.Contains(out, "agent-alias-missing-skill") {
		t.Errorf("validate should report the unresolved executor-path skill (proving agent= parsed as executor):\n%s", out)
	}
}

// TestAgentsTomlBootsEndToEnd proves the agents.toml back-compat loader end-to-end
// (sty_536f9960): with ONLY the canonical agents.toml present (the legacy
// actors.toml removed), the real binary boots, indexes, and reports status cleanly
// — applyActorGrants resolves the [reviewer] binding from agents.toml on store
// open. It is the agents.toml counterpart to TestReviewerModelActorsBoots, proving
// the binary no longer depends on the actors.toml filename.
func TestAgentsTomlBootsEndToEnd(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// init scaffolds the canonical agents.toml; overwrite it with a reviewer-model
	// binding. With no legacy actors.toml present, a loader that ignored agents.toml
	// would resolve no binding at all.
	writeFile(t, filepath.Join(repo, ".satelle", "agents.toml"), "[reviewer]\nmodel = \"sonnet\"\n")
	mustRun(t, testBin, repo, "index")
	out := mustRun(t, testBin, repo, "status")
	if !strings.Contains(out, "repo root") {
		t.Errorf("status should boot cleanly with only agents.toml present:\n%s", out)
	}
}

// deprecatedActorWF is a wildcard workflow declaring its executor node with the
// retired actor= keyword — which validate must now reject (sty_7db2ed7d).
func deprecatedActorWF(name string) string {
	return "---\nname: " + name + "\ntype: workflow\napplies_to: [\"*\"]\ndescription: a workflow using the retired actor keyword\n---\n" +
		"```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  in_progress [actor=executor]
  done [shape=Msquare, agent=reviewer]
  cancelled [agent=reviewer]
  backlog -> in_progress
  in_progress -> done
  backlog -> cancelled
}` + "\n```\n"
}

// TestValidateRejectsActorKeyword proves the rename is ENFORCED end-to-end
// (sty_7db2ed7d): a workflow authored with the retired actor= keyword fails
// `satelle validate` with an actionable message, so the rename cannot silently
// regress.
func TestValidateRejectsActorKeyword(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "legacy-kw.md"), deprecatedActorWF("legacy-kw"))
	mustRun(t, testBin, repo, "index")

	out, err := run(t, testBin, repo, "validate", "workflows")
	if err == nil {
		t.Fatalf("validate should fail on a workflow using the retired actor= keyword:\n%s", out)
	}
	if !strings.Contains(out, `deprecated "actor"`) {
		t.Errorf("validate should name the deprecated actor keyword:\n%s", out)
	}
}

// TestValidateFlagsActorsToml proves validate flags the retired actors.toml
// filename (sty_7db2ed7d): a repo still carrying it is silently on defaults, so
// validate fails telling the operator to rename it to agents.toml.
func TestValidateFlagsActorsToml(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// init scaffolds agents.toml; drop a legacy actors.toml beside it.
	writeFile(t, filepath.Join(repo, ".satelle", "actors.toml"), "[reviewer]\nmodel = \"sonnet\"\n")
	mustRun(t, testBin, repo, "index")

	out, err := run(t, testBin, repo, "validate")
	if err == nil {
		t.Fatalf("validate should fail on a repo still carrying actors.toml:\n%s", out)
	}
	if !strings.Contains(out, "actors.toml") || !strings.Contains(out, "agents.toml") {
		t.Errorf("validate should name actors.toml and the agents.toml fix:\n%s", out)
	}
}
