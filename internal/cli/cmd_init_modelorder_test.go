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

var modelOrderExecutables = []string{"claude", "grok"}

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

// TestScaffoldAgentsTomlWritesAllModelOrders (sty_4fde0a50 AC4): init's
// agents.toml carries a non-empty order for each of claude and grok.
func TestScaffoldAgentsTomlWritesAllModelOrders(t *testing.T) {
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
// executables' orders, and they equal what init writes.
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

var usesAll = map[string]bool{"claude": true, "grok": true}

func TestWithModelOrderHealsOnlyWhenAbsent(t *testing.T) {
	old := "[executor]\ncommand = \"in-loop\"\n"
	healed, notes := withModelOrder(old, usesAll, nil)
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
	if got, n := withModelOrder(own, usesAll, nil); got != own || len(n) != 0 {
		t.Errorf("existing [model_order] was touched: %q %v", got, n)
	}
	// Idempotent on its own output.
	if got, n := withModelOrder(healed, usesAll, nil); got != healed || len(n) != 0 {
		t.Error("heal is not idempotent")
	}
}

// TestWithModelOrderSeedsOnlyUsedExecutables (sty_a4b90ede AC2): a heal seeds
// lists only for executables the bindings spawn.
func TestWithModelOrderSeedsOnlyUsedExecutables(t *testing.T) {
	old := "[reviewer]\nrole = \"reviewer\"\n"
	healed, notes := withModelOrder(old, map[string]bool{"claude": true}, nil)
	if len(notes) != 1 {
		t.Fatalf("notes = %v, want one", notes)
	}
	ac := loadScaffoldAgents(t, healed)
	if len(ac.OrderFor("claude")) == 0 {
		t.Error("claude-only heal has no claude list")
	}
	if got := ac.OrderFor("grok"); len(got) != 0 {
		t.Errorf("claude-only heal seeded a grok list: %v\n%s", got, healed)
	}
	if strings.Contains(healed, "grok   =") {
		t.Errorf("claude-only heal wrote a grok line:\n%s", healed)
	}

	both, _ := withModelOrder(old, usesAll, nil)
	ac = loadScaffoldAgents(t, both)
	for _, exe := range modelOrderExecutables {
		if len(ac.OrderFor(exe)) == 0 {
			t.Errorf("claude+grok heal has no %s list", exe)
		}
	}
}

// TestWithModelOrderNoSeededExecutableAppendsNothing (sty_a4b90ede AC3): when
// the bindings spawn no executable with a seeded list, nothing is appended and
// no note is reported.
func TestWithModelOrderNoSeededExecutableAppendsNothing(t *testing.T) {
	old := "[executor]\ncommand = \"in-loop\"\n"
	for _, uses := range []map[string]bool{nil, {}, {"codex": true}} {
		if got, n := withModelOrder(old, uses, nil); got != old || len(n) != 0 {
			t.Errorf("uses=%v: got %q notes %v, want unchanged and no note", uses, got, n)
		}
	}
}

func writeInitAgents(t *testing.T, repo, body string) string {
	t.Helper()
	dir := filepath.Join(repo, ".satelle", config.AgentsConfigDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, config.AgentsConfigName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestInitKeepsOwnModelOrder (sty_a4b90ede AC1): init over a repo whose
// agents.toml already declares [model_order] leaves the file byte-identical and
// reports no note for it.
func TestInitKeepsOwnModelOrder(t *testing.T) {
	repo := t.TempDir()
	body := "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\n\n[model_order]\nclaude = [\"haiku\"]\n"
	path := writeInitAgents(t, repo, body)
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("agents.toml changed on init:\n%s", got)
	}
	if strings.Contains(out.String(), "added [model_order]") {
		t.Errorf("init reported a model_order heal:\n%s", out.String())
	}
}

// TestInitHealSeedsOnlyBoundExecutables (sty_a4b90ede AC2): init heals an
// agents.toml without [model_order] whose reviewer is bound through a catalog
// profile= to claude and that names no grok command; the healed file has a
// claude list and no grok list.
func TestInitHealSeedsOnlyBoundExecutables(t *testing.T) {
	repo := t.TempDir()
	isolateUserHome(t)
	catalog := "[profiles.claude-opus]\nrole = \"reviewer\"\ncommand = \"claude -p --append-system-prompt {system} --model {model}\"\ntools = \"Read,Grep,Glob\"\n"
	if err := os.WriteFile(config.GlobalAgentsPath(), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	path := writeInitAgents(t, repo, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\nprofile = \"claude-opus\"\n")
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ac := loadScaffoldAgents(t, string(got))
	if len(ac.OrderFor("claude")) == 0 {
		t.Errorf("healed agents.toml has no claude list:\n%s", got)
	}
	if o := ac.OrderFor("grok"); len(o) != 0 {
		t.Errorf("healed agents.toml seeded a grok list %v:\n%s", o, got)
	}
	if !strings.Contains(out.String(), "added [model_order]") {
		t.Errorf("init did not report the heal:\n%s", out.String())
	}
}
