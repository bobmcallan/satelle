//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitSeedsCommentedReworkBindingsAndDocs pins the init seed after
// sty_462603f0: a fresh agents.toml does not teach a live orchestrator or
// story chat as the driver. A commented one-shot [coder] may remain. The
// shipped baseline still grants the seats the file does not name. The seeded
// workflows README documents rework = { consult, rounds }.
func TestInitSeedsCommentedReworkBindingsAndDocs(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	agentsPath := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	agents, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read agents.toml: %v", err)
	}
	body := string(agents)
	for _, want := range []string{
		"driving session is the in-loop executor",
		"# [coder]",
		"ONE-SHOT coder",
		"satelle story rework",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("seeded agents.toml missing %q", want)
		}
	}
	for _, bad := range []string{"# [orchestrator]", "satelle story chat", "\n[orchestrator]\n", "\n[coder]\n"} {
		if strings.Contains(body, bad) {
			t.Errorf("seeded agents.toml must not contain %q", strings.TrimSpace(bad))
		}
	}

	out := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(out, "PASS  agent validate green") {
		t.Errorf("fresh init with commented examples must validate healthy:\n%s", out)
	}
	// The seed keeps the tables commented. The grants come from the baseline,
	// which is what runs for a seat the repo file does not name.
	for _, seat := range []string{"orchestrator", "coder"} {
		if !strings.Contains(out, "GRANT ["+seat+"]") {
			t.Errorf("baseline seat %s must be granted:\n%s", seat, out)
		}
	}
	if !strings.Contains(out, `(baseline)`) {
		t.Errorf("baseline seats must record baseline provenance:\n%s", out)
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
