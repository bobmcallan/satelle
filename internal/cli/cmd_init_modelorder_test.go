package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/help"
)

var modelOrderExecutables = []string{"claude", "grok", "codex"}

func loadScaffoldAgents(t *testing.T, content string) config.AgentsConfig {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, config.AgentsConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.AgentsConfigDir, config.AgentsConfigName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ac, err := config.LoadAgents(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return ac
}

// TestScaffoldAgentsTomlWritesAllThreeModelOrders (sty_4fde0a50 AC4): init's
// agents.toml carries a non-empty order for each of claude, grok and codex.
func TestScaffoldAgentsTomlWritesAllThreeModelOrders(t *testing.T) {
	ac := loadScaffoldAgents(t, scaffoldAgentsToml)
	for _, exe := range modelOrderExecutables {
		if len(ac.OrderFor(exe)) == 0 {
			t.Errorf("init scaffold has no [model_order] list for %s", exe)
		}
	}
	if _, ok := ac.Agents["model_order"]; ok {
		t.Error("[model_order] read as a binding")
	}
}

// TestAgentDispatchHelpListsSameModelOrders (AC4): the help topic lists all
// three executables' orders, and they equal what init writes.
func TestAgentDispatchHelpListsSameModelOrders(t *testing.T) {
	top, ok := help.Get("agent-dispatch")
	if !ok {
		t.Fatal("agent-dispatch topic not found")
	}
	ac := loadScaffoldAgents(t, scaffoldAgentsToml)
	for _, exe := range modelOrderExecutables {
		var names []string
		for _, r := range ac.OrderFor(exe) {
			names = append(names, strings.Join(r, "/"))
		}
		row := fmt.Sprintf("| %s | %s |", exe, strings.Join(names, ", "))
		if !strings.Contains(top.Body, row) {
			t.Errorf("agent-dispatch help is missing or differs from init for %s: want row %q", exe, row)
		}
	}
}

func TestWithModelOrderHealsOnlyWhenAbsent(t *testing.T) {
	old := "[executor]\ncommand = \"in-loop\"\n"
	healed, notes := withModelOrder(old, nil)
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want one", notes)
	}
	ac := loadScaffoldAgents(t, healed)
	for _, exe := range modelOrderExecutables {
		if len(ac.OrderFor(exe)) == 0 {
			t.Errorf("healed file has no %s list", exe)
		}
	}
	// An operator's own table is never overwritten or duplicated.
	own := old + "\n[model_order]\nclaude = [\"haiku\"]\n"
	if got, n := withModelOrder(own, nil); got != own || len(n) != 0 {
		t.Errorf("existing [model_order] was touched: %q %v", got, n)
	}
	// Idempotent on its own output.
	if got, n := withModelOrder(healed, nil); got != healed || len(n) != 0 {
		t.Error("heal is not idempotent")
	}
}
