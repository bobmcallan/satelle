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
	// Live-capable examples stay commented (sty_cec967b5): a fresh repo's
	// behaviour is unchanged until the operator uncomments them.
	for _, want := range []string{"# [orchestrator]", "# [coder]", "satelle story rework", "interface = \"acp\" or \"stream\""} {
		if !strings.Contains(scaffoldAgentsToml, want) {
			t.Errorf("scaffold missing commented live-capable example %q", want)
		}
	}
	if strings.Contains(scaffoldAgentsToml, "\n[orchestrator]\n") || strings.Contains(scaffoldAgentsToml, "\n[coder]\n") {
		t.Error("scaffold [orchestrator]/[coder] examples must stay fully commented")
	}
	// Failed Jev/typesafe prototype removed (sty_e3eca0b7): scaffold must not
	// ship a dormant reviewer-typesafe / interface=typesafe switch.
	for _, ban := range []string{
		"reviewer-typesafe",
		`interface  = "typesafe"`,
		`interface = "typesafe"`,
	} {
		if strings.Contains(scaffoldAgentsToml, ban) {
			t.Errorf("scaffold must not contain %q", ban)
		}
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
	// idle_timeout / timeout documentation (sty_752c4ef2 AC8): the scaffold must
	// teach the stall detector — idle_timeout is what bounds a dispatch, timeout
	// is the optional unset-by-default hard ceiling, and a stall is its own
	// named outcome — so an operator editing the file never reads it as a
	// wall-clock cap.
	for _, want := range []string{
		`idle_timeout    = "5m"`,
		"STALLED",
		"heartbeats alone never reset it",
		"idle_timeout is what actually bounds",
		"OPTIONAL hard ceiling",
		"UNSET by default",
		`"stalled"`,
	} {
		if !strings.Contains(scaffoldAgentsToml, want) {
			t.Errorf("scaffold missing idle_timeout/timeout doc %q", want)
		}
	}
	// Model selection (sty_7069bced): the scaffold must document that an empty
	// model= is selected deliberately, not left to the CLI's own default, and
	// must ship a commented [models] ranking example pointing at the help topic.
	for _, want := range []string{
		"selected deliberately, not left to the CLI's own default",
		"§ Model selection",
		"# [models]",
		`# ranking = ["opus", "sonnet", "haiku"]`,
	} {
		if !strings.Contains(scaffoldAgentsToml, want) {
			t.Errorf("scaffold missing model-selection doc %q", want)
		}
	}
	if strings.Contains(scaffoldAgentsToml, "\n[models]\n") {
		t.Error("scaffold [models] ranking example must stay commented")
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
