package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
)

const solidsafeStyle = `# agents.toml — Grok-native bindings (dual payload: {payload} on -p + stdin).
# Reviewer/planner use plain output so gate verdicts parse.

[executor]
harness = "in-loop"

[reviewer]
harness = "grok -p {payload} --system-prompt-override {system} --tools {tools} -m {model} --deny Write --deny Edit --always-approve --output-format plain --max-turns 12 --no-subagents"
tools   = "read_file,grep,list_dir"
model   = "grok-4.5"

[planner]
harness = "grok -p {payload} --system-prompt-override {system} --tools {tools} -m {model} --always-approve --output-format plain --max-turns 16 --no-subagents"
tools   = "read_file,grep,list_dir,run_terminal_command"
model   = "grok-4.5"

[worker]
harness = "grok -p {payload} --system-prompt-override {system} --tools {tools} -m {model} --always-approve --output-format plain --max-turns 24 --no-subagents"
tools   = "read_file,grep,list_dir,search_replace,write"
model   = "grok-4.5"
timeout = "45m"

[retrospective]
harness = "grok -p {payload} --system-prompt-override {system} --tools {tools} -m {model} --always-approve --output-format plain --max-turns 16 --no-subagents"
tools   = "read_file,grep,list_dir"
model   = "grok-4.5"
`

func TestMigrateAgentsHarnessToCommand(t *testing.T) {
	in := "[executor]\nharness = \"in-loop\"\n\n[reviewer]\ncommand = \"claude\"\nrole = \"reviewer\"\n"
	out, changes, err := MigrateAgents(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `command = "in-loop"`) {
		t.Fatalf("harness not rewritten:\n%s", out)
	}
	if strings.Contains(out, "harness") {
		t.Fatalf("harness still present:\n%s", out)
	}
	// bare command=claude expands to the full template
	if !strings.Contains(out, `command = "`+agentcli.DefaultClaudeCommand+`"`) {
		t.Fatalf("bare claude not expanded:\n%s", out)
	}
	if len(changes) == 0 {
		t.Fatal("expected change notes")
	}
}

func TestMigrateAgentsAddsRole(t *testing.T) {
	in := "[executor]\ncommand = \"in-loop\"\n\n[reviewer]\ncommand = \"claude\"\n"
	out, _, err := MigrateAgents(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `role = "agent"`) {
		t.Fatalf("executor missing role=agent:\n%s", out)
	}
	if !strings.Contains(out, `role = "reviewer"`) {
		t.Fatalf("reviewer missing role=reviewer:\n%s", out)
	}
	// declared role untouched
	withRole := "[executor]\ncommand = \"in-loop\"\nrole = \"agent\" # keep\n"
	out2, ch, err := MigrateAgents(withRole)
	if err != nil {
		t.Fatal(err)
	}
	if out2 != withRole {
		t.Fatalf("declared role must be byte-identical:\n got %q\nwant %q", out2, withRole)
	}
	if len(ch) != 0 {
		t.Fatalf("no changes expected, got %v", ch)
	}
}

func TestMigrateAgentsPreservesComments(t *testing.T) {
	in := "# header comment\n\n[executor]\n# inline section note\nharness = \"in-loop\"\ntools = \"\"\n"
	out, _, err := MigrateAgents(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# header comment") || !strings.Contains(out, "# inline section note") {
		t.Fatalf("comments lost:\n%s", out)
	}
	if !strings.Contains(out, `command = "in-loop"`) {
		t.Fatalf("migration missing:\n%s", out)
	}
}

func TestMigrateAgentsUnparseable(t *testing.T) {
	in := "[[[broken"
	out, changes, err := MigrateAgents(in)
	if err == nil {
		t.Fatal("want parse error")
	}
	if out != "" || changes != nil {
		t.Fatalf("on parse error out=%q changes=%v", out, changes)
	}
}

func TestMigrateAgentsIdempotentCanonical(t *testing.T) {
	// scaffold-like: already full command= + role= (no bare presets)
	canonical := `[executor]
role    = "agent"
command = "in-loop"

[reviewer]
role    = "reviewer"
command = "` + agentcli.DefaultClaudeCommand + `"
tools   = "Read,Grep,Glob"
`
	out, ch, err := MigrateAgents(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 0 {
		t.Fatalf("canonical should be no-op, changes=%v", ch)
	}
	if out != canonical {
		t.Fatalf("canonical body mutated")
	}
	// second pass on solidsafe-style
	once, _, err := MigrateAgents(solidsafeStyle)
	if err != nil {
		t.Fatal(err)
	}
	twice, ch2, err := MigrateAgents(once)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch2) != 0 || twice != once {
		t.Fatalf("second pass not idempotent: ch=%v", ch2)
	}
}

// Bare command="claude" / harness="grok" expand to full templates; a second
// MigrateAgents pass is a byte-for-byte no-op (AC3, sty_6752e35b).
func TestMigrateAgentsExpandBarePreset(t *testing.T) {
	in := `[executor]
harness = "in-loop"

[reviewer]
command = "claude"

[planner]
harness = "grok"
`
	once, changes, err := MigrateAgents(in)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(changes, ",")
	if !strings.Contains(joined, "expand bare preset") {
		t.Fatalf("expected expand note, got %v", changes)
	}
	if !strings.Contains(once, agentcli.DefaultClaudeCommand) {
		t.Fatalf("claude not expanded:\n%s", once)
	}
	if !strings.Contains(once, agentcli.DefaultGrokCommand) {
		t.Fatalf("grok not expanded:\n%s", once)
	}
	if strings.Contains(once, `command = "claude"`) || strings.Contains(once, `command = "grok"`) {
		t.Fatalf("bare tokens remain:\n%s", once)
	}
	twice, ch2, err := MigrateAgents(once)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch2) != 0 || twice != once {
		t.Fatalf("second pass not idempotent: ch=%v\nonce==twice=%v", ch2, twice == once)
	}
}

func TestMigrateAgentsSolidsafeZeroRoleInferred(t *testing.T) {
	out, changes, err := MigrateAgents(solidsafeStyle)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) == 0 {
		t.Fatal("solidsafe-style should need migration")
	}
	// Every section declares role= — RoleInferred false (silences agentvalidate WARN).
	dir := t.TempDir()
	path := filepath.Join(dir, AgentsConfigName)
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := LoadAgents(dir)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string]AgentBinding{
		"executor": ac.Executor, "reviewer": ac.Reviewer,
	} {
		if RoleInferred(b) {
			t.Errorf("%s still has RoleInferred after migrate", name)
		}
	}
	for name, b := range ac.Agents {
		if RoleInferred(b) {
			t.Errorf("%s still has RoleInferred after migrate", name)
		}
	}
	if strings.Contains(out, "harness") {
		t.Fatalf("harness remnants after migrate:\n%s", out)
	}
}
