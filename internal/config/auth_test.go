package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auth = "subscription" | "api_key" | "org" is declared per binding; a binding
// with none resolves to unrecorded, and any other value is refused at load naming
// the field, in the repo file and the machine catalog alike.
func TestBindingAuthKey(t *testing.T) {
	load := func(t *testing.T, body string) (AgentsConfig, error) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return LoadAgents(dir)
	}

	for _, good := range []string{AuthSubscription, AuthAPIKey, AuthOrg} {
		ac, err := load(t, "[reviewer]\ncommand = \"verdict.sh -p {payload}\"\nauth = \""+good+"\"\n")
		if err != nil {
			t.Fatalf("auth %q refused: %v", good, err)
		}
		if got := ac.Reviewer.ResolvedAuth(); got != good {
			t.Errorf("auth %q resolved to %q", good, got)
		}
	}

	ac, err := load(t, "[reviewer]\ncommand = \"verdict.sh -p {payload}\"\n")
	if err != nil {
		t.Fatalf("an absent auth must load: %v", err)
	}
	if got := ac.Reviewer.ResolvedAuth(); got != AuthUnrecorded {
		t.Errorf("a binding with no auth resolved to %q, want %q", got, AuthUnrecorded)
	}
	if got := (AgentBinding{Auth: "  "}).ResolvedAuth(); got != AuthUnrecorded {
		t.Errorf("a blank auth resolved to %q, want %q", got, AuthUnrecorded)
	}

	for _, bad := range []string{"free", "unrecorded", "key"} {
		_, err := load(t, "[reviewer]\ncommand = \"verdict.sh -p {payload}\"\nauth = \""+bad+"\"\n")
		if err == nil || !strings.Contains(err.Error(), "auth") || !strings.Contains(err.Error(), AuthAPIKey) {
			t.Errorf("auth %q: want a load error naming the field and the valid values, got %v", bad, err)
		}
		if _, err := ParseGlobalAgents("[profiles.p]\ncommand = \"verdict.sh -p {payload}\"\nauth = \"" + bad + "\"\n"); err == nil {
			t.Errorf("catalog auth %q must be refused", bad)
		}
	}

	g, err := ParseGlobalAgents("[profiles.p]\ncommand = \"verdict.sh -p {payload}\"\nauth = \"org\"\n")
	if err != nil {
		t.Fatalf("catalog auth refused: %v", err)
	}
	if got := g.Profiles["p"].ResolvedAuth(); got != AuthOrg {
		t.Errorf("catalog auth resolved to %q", got)
	}
}
