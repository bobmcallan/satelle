package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
)

// TestScaffoldAgentsTomlFullyDefined proves the agents layer ships fully
// defined at init (sty_892517e7): the scaffold carries ACTIVE [executor] and
// [reviewer] sections whose values match the coded defaults — no hidden coded
// configuration — and the parsed bindings equal the absent-file defaults.
func TestScaffoldAgentsTomlFullyDefined(t *testing.T) {
	for _, want := range []string{
		"[executor]", `command = "in-loop"`,
		"[reviewer]", agentcli.DefaultClaudeCommand, `tools   = "Read,Grep,Glob"`,
		// sty_5f1d7b2e: role= is the declared contract; inference is a fallback.
		`role    = "agent"`, `role    = "reviewer"`,
	} {
		if !strings.Contains(scaffoldAgentsToml, want) {
			t.Errorf("scaffold missing active entry %q", want)
		}
	}
	// Commented named-agent example must declare role= so copy-paste starts clean.
	if !strings.Contains(scaffoldAgentsToml, `# role    = "agent"`) {
		t.Error(`scaffold commented [commit-agent] missing # role    = "agent"`)
	}
	// The scaffold header must DOCUMENT the preset menu + placeholder grammar
	// (AC4, sty_17cae74b) so an operator editing the file sees the choices without
	// reading the source: the four presets and the argv placeholders.
	for _, want := range []string{
		"claude", "grok", "codex", "in-loop", // the preset menu
		"{system}", "{tools}", "{model}", "{payload}", // the placeholder grammar
		// role= declared contract note (sty_5f1d7b2e)
		`role= is the binding's declared contract`,
	} {
		if !strings.Contains(scaffoldAgentsToml, want) {
			t.Errorf("scaffold header missing preset/placeholder doc %q", want)
		}
	}
	// Parity: loading the scaffold yields the same effective reviewer binding as
	// the coded defaults for an absent file.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.AgentsConfigName), []byte(scaffoldAgentsToml), 0o644); err != nil {
		t.Fatal(err)
	}
	ag, err := config.LoadAgents(dir)
	if err != nil {
		t.Fatalf("scaffold does not parse: %v", err)
	}
	rev := ag.ReviewerBinding()
	// The written harness is the FULL command template (transparent, swappable) —
	// exactly what the bare "claude" preset expands to, so behaviour is unchanged.
	if rev.Command != agentcli.DefaultClaudeCommand || rev.Tools != config.DefaultReviewerTools {
		t.Errorf("scaffold reviewer = (%q, %q), want (%q, %q)",
			rev.Command, rev.Tools, agentcli.DefaultClaudeCommand, config.DefaultReviewerTools)
	}
	// Scaffold alone must validate with zero warnings/problems (no role inference).
	report := agentvalidate.Validate(ag, nil, nil)
	if len(report.Warnings) > 0 || len(report.Problems) > 0 {
		t.Errorf("scaffold agents layer must validate clean; warnings=%v problems=%v",
			report.Warnings, report.Problems)
	}
}
