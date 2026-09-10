//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitSeedsCommentedReworkBindingsAndDocs pins sty_cec967b5 AC1/AC2:
// after satelle init, agents.toml carries commented [orchestrator]/[coder]
// examples (inert — validate stays healthy and does not grant them), and the
// seeded workflows README documents rework = { consult, rounds }.
func TestInitSeedsCommentedReworkBindingsAndDocs(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	agentsPath := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	agents, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read agents.toml: %v", err)
	}
	body := string(agents)
	for _, want := range []string{"# [orchestrator]", "# [coder]", "interface = \"stream\"", "satelle story rework"} {
		if !strings.Contains(body, want) {
			t.Errorf("seeded agents.toml missing %q", want)
		}
	}
	// Uncommented live tables must not appear — comments stay comments.
	for _, bad := range []string{"\n[orchestrator]\n", "\n[coder]\n"} {
		if strings.Contains(body, bad) {
			t.Errorf("seeded agents.toml must keep %q commented; found live table", strings.TrimSpace(bad))
		}
	}

	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, "PASS  agent validate green") {
		t.Errorf("fresh init with commented examples must validate healthy:\n%s", out)
	}
	if strings.Contains(out, "GRANT [orchestrator]") || strings.Contains(out, "GRANT [coder]") {
		t.Errorf("commented examples must not appear as grants:\n%s", out)
	}
	for _, want := range []string{"GRANT [executor]", "GRANT [reviewer]"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate should still list %s:\n%s", want, out)
		}
	}

	readme, err := os.ReadFile(filepath.Join(repo, ".satelle", "workflows", "README.md"))
	if err != nil {
		t.Fatalf("read workflows README: %v", err)
	}
	rbody := string(readme)
	for _, want := range []string{"rework", "consult", "rounds", "Absent means", "cold", "READY"} {
		if !strings.Contains(rbody, want) {
			t.Errorf("workflows README missing %q:\n%s", want, rbody)
		}
	}

	// step.toml is virtual-by-default (init must not seed it); AC2's step.toml
	// half is covered by the embedded-source unit test in internal/config.
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "workflows", "step.toml")); err == nil {
		t.Error("init must not seed step.toml (virtual default); do not assert on-disk comments here")
	}
}
