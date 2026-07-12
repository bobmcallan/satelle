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
	// The scaffold header must DOCUMENT full-template requirement + placeholders
	// (AC4, sty_6752e35b) so an operator editing the file sees that bare presets
	// are rejected and only in-loop is a valid single token.
	for _, want := range []string{
		"in-loop",                                     // the only bare single-token value
		"FULL multi-token command",                    // full template required
		"{system}", "{tools}", "{model}", "{payload}", // the placeholder grammar
		// role= declared contract note (sty_5f1d7b2e)
		`role= is the binding's declared contract`,
	} {
		if !strings.Contains(scaffoldAgentsToml, want) {
			t.Errorf("scaffold header missing template/placeholder doc %q", want)
		}
	}
	// Must NOT advertise bare presets as valid bindings.
	if strings.Contains(scaffoldAgentsToml, "SINGLE token is a built-in PRESET") {
		t.Error("scaffold must not advertise bare CLI presets")
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
