package config

import (
	"strings"
	"testing"
)

// A bare grok expands to the template of the role ResolvedRole gives its section:
// the reviewer cap for a reviewer, the original cap for any other binding.
func TestMigrateAgentsGrokTurnBudgetByRole(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"reviewer section", "[reviewer]\ncommand = \"grok\"\n", "--max-turns 32"},
		{"legacy nested reviewer", "[agents.reviewer]\ncommand = \"grok\"\n", "--max-turns 32"},
		{"declared reviewer role", "[judge]\nrole = \"reviewer\"\ncommand = \"grok\"\n", "--max-turns 32"},
		{"executor section", "[executor]\ncommand = \"grok\"\n", "--max-turns 16"},
		{"declared agent role", "[reviewer]\nrole = \"agent\"\ncommand = \"grok\"\n", "--max-turns 16"},
		{"planner section", "[planner]\nharness = \"grok\"\n", "--max-turns 16"},
	}
	for _, c := range cases {
		out, _, err := MigrateAgents(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: want %q in:\n%s", c.name, c.want, out)
		}
	}
}

// The grok starter catalog seeds the reviewer profile with the reviewer turn cap
// and the agent profile with the original one.
func TestStarterGlobalAgentsGrokTurnBudgetByRole(t *testing.T) {
	body, err := StarterGlobalAgents("grok")
	if err != nil {
		t.Fatal(err)
	}
	g, err := ParseGlobalAgents(body)
	if err != nil {
		t.Fatal(err)
	}
	for profile, want := range map[string]string{"grok-reviewer": "--max-turns 32", "grok-agent": "--max-turns 16"} {
		cmd := g.Profiles[profile].Command
		if !strings.Contains(cmd, want) {
			t.Errorf("[profiles.%s] command lacks %q: %s", profile, want, cmd)
		}
	}
}
