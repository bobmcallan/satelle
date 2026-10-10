package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// AC2 (sty_f141c77f): a repository that declares no absent-wiring policy gets
// fail-closed, and the embedded defaults declare wiring for the harnesses
// satelle deploys and none for unknown.
func TestAbsentWiringPolicyDefaultsToRefuse(t *testing.T) {
	if got := (Config{}).AbsentWiringPolicy(); got != AbsentWiringRefuse {
		t.Fatalf("undeclared policy = %q, want %q", got, AbsentWiringRefuse)
	}
	var c Config
	c.Worktree.AbsentWiring = AbsentWiringFailOpen
	if got := c.AbsentWiringPolicy(); got != AbsentWiringFailOpen {
		t.Fatalf("declared policy = %q", got)
	}
}

func TestGateWiringResolution(t *testing.T) {
	for harness, want := range map[string]string{
		"claude": ".claude/settings.json",
		"grok":   ".grok/hooks/satelle.json",
		"pi":     ".pi/extensions/satelle.ts",
		"cursor": ".cursor/hooks.json",
	} {
		got := (Config{}).GateWiring(harness)
		if len(got) != 1 || got[0] != want {
			t.Errorf("embedded GateWiring(%q) = %v, want [%s]", harness, got, want)
		}
	}
	if got := (Config{}).GateWiring("unknown"); len(got) != 0 {
		t.Errorf("unknown declares no wiring, got %v", got)
	}
	if got := (Config{}).GateWiring("mybot"); len(got) != 0 {
		t.Errorf("an undeclared harness has no wiring and never falls through, got %v", got)
	}
	c := Config{Harness: map[string]HarnessConfig{
		"claude":  {GateWiring: []string{"a/b"}},
		"unknown": {GateWiring: []string{"u"}},
	}}
	if got := c.GateWiring("CLAUDE"); len(got) != 1 || got[0] != "a/b" {
		t.Errorf("repo override = %v", got)
	}
	if got := c.GateWiring("unknown"); len(got) != 1 || got[0] != "u" {
		t.Errorf("repo-declared unknown wiring = %v", got)
	}
	if got := c.GateWiring("grok"); len(got) != 1 || got[0] != ".grok/hooks/satelle.json" {
		t.Errorf("an untouched harness keeps its embedded wiring, got %v", got)
	}
}

// AC2: an unknown policy value, an unknown key under [worktree] or
// [harness.<name>], or a malformed wiring entry is refused at Load naming the
// field, and nothing under the repo is rewritten.
func TestLoad_RefusesBadAbsentWiringDeclarations(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"unknown policy", "[worktree]\nabsent_wiring = \"allow\"\n", []string{"[worktree] absent_wiring", `"allow"`}},
		{"policy wrong case", "[worktree]\nabsent_wiring = \"Fail-Open\"\n", []string{"[worktree] absent_wiring"}},
		{"unknown worktree key", "[worktree]\nabsent_wirng = \"refuse\"\n", []string{"unknown key", "[worktree] absent_wirng"}},
		{"unknown harness key", "[harness.claude]\ngate_wirng = [\".claude/settings.json\"]\n", []string{"unknown key", "[harness.claude] gate_wirng"}},
		{"empty entry", "[harness.claude]\ngate_wiring = [\"\"]\n", []string{"[harness.claude] gate_wiring", "empty"}},
		{"pattern entry", "[harness.grok]\ngate_wiring = [\".grok/*.json\"]\n", []string{"[harness.grok] gate_wiring", "pattern"}},
		{"absolute entry", "[harness.pi]\ngate_wiring = [\"/etc/passwd\"]\n", []string{"[harness.pi] gate_wiring", "relative"}},
		{"escaping entry", "[harness.pi]\ngate_wiring = [\"../x\"]\n", []string{"[harness.pi] gate_wiring", "outside the repository"}},
		{"git entry", "[harness.pi]\ngate_wiring = [\".git/config\"]\n", []string{"[harness.pi] gate_wiring", "git"}},
		{"whole repo", "[harness.pi]\ngate_wiring = [\".\"]\n", []string{"[harness.pi] gate_wiring", "whole repository"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeWorktreeRepo(t, tc.body)
			root := RepoRootFromConfigPath(p)
			before := treeHash(t, root)
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
			stBefore, _ := os.Stat(p)
			_, _, err := Load(p)
			if err == nil {
				t.Fatalf("Load accepted %q", tc.body)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not name %q", err, w)
				}
			}
			if after := treeHash(t, root); after != before {
				t.Error("a refusal rewrote a file under the repo")
			}
			if stAfter, _ := os.Stat(p); !stAfter.ModTime().Equal(stBefore.ModTime()) {
				t.Error("a refusal touched satelle.toml")
			}
		})
	}
}

// The same strictness applies to the per-user satelle.local.toml overlay.
func TestLoad_RefusesUnknownKeyInLocalOverlay(t *testing.T) {
	p := writeWorktreeRepo(t, "[worktree]\nabsent_wiring = \"refuse\"\n")
	local := RepoRootFromConfigPath(p) + "/" + DefaultDataDir + "/" + LocalConfigName
	if err := os.WriteFile(local, []byte("[harness.claude]\nbogus = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "[harness.claude] bogus") {
		t.Fatalf("overlay unknown key: %v", err)
	}
}

// Valid declarations load, and unrelated tables stay lenient.
func TestLoad_AcceptsWiringDeclarations(t *testing.T) {
	p := writeWorktreeRepo(t, "[worktree]\nabsent_wiring = \"fail-open\"\n\n[harness.mybot]\ngate_wiring = [\".mybot/hooks.json\"]\ncontext_limit_bytes = 100\n\n[some_other_table]\nanything = 1\n")
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AbsentWiringPolicy() != AbsentWiringFailOpen {
		t.Errorf("policy = %q", cfg.AbsentWiringPolicy())
	}
	if got := cfg.GateWiring("mybot"); len(got) != 1 || got[0] != ".mybot/hooks.json" {
		t.Errorf("GateWiring(mybot) = %v", got)
	}
}
