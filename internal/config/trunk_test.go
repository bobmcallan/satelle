package config

import (
	"strings"
	"testing"
)

func TestLoad_TrunkAbsentTableChecksAndRefusesDirtyAndDiverged(t *testing.T) {
	cfg, _, err := Load(writeWorktreeRepo(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Trunk.Enabled() {
		t.Error("an absent [trunk] table must leave the check on")
	}
	if got := strings.Join(cfg.Trunk.RefuseSet(), ","); got != "dirty,diverged" {
		t.Errorf("default refuse = %q, want dirty,diverged", got)
	}
	for state, want := range map[string]bool{"dirty": true, "diverged": true, "behind": false, "ahead": false, "offline": false, "level": false} {
		if got := cfg.Trunk.Refuses(state); got != want {
			t.Errorf("Refuses(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestLoad_TrunkKeysAbsentWithinATableKeepTheDefaults(t *testing.T) {
	cfg, _, err := Load(writeWorktreeRepo(t, "[trunk]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Trunk.Enabled() || !cfg.Trunk.Refuses("dirty") || !cfg.Trunk.Refuses("diverged") {
		t.Errorf("an empty [trunk] table changed the defaults: %+v", cfg.Trunk)
	}
}

func TestLoad_TrunkCheckFalseDisables(t *testing.T) {
	cfg, _, err := Load(writeWorktreeRepo(t, "[trunk]\ncheck = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Trunk.Enabled() {
		t.Error("[trunk] check = false must disable the check")
	}
}

func TestLoad_TrunkRefuseOverride(t *testing.T) {
	cfg, _, err := Load(writeWorktreeRepo(t, "[trunk]\nrefuse = [\"dirty\", \"behind\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Trunk.Refuses("dirty") || !cfg.Trunk.Refuses("behind") || cfg.Trunk.Refuses("diverged") {
		t.Errorf("refuse override not honoured: %v", cfg.Trunk.RefuseSet())
	}
	empty, _, err := Load(writeWorktreeRepo(t, "[trunk]\nrefuse = []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if empty.Trunk.Refuses("dirty") || empty.Trunk.Refuses("diverged") {
		t.Errorf("an explicit empty refuse list must refuse nothing: %v", empty.Trunk.RefuseSet())
	}
}

// base_refuse (sty_92337a13) is wider than refuse by default and is declared
// on its own, so the engage-time default does not move.
func TestLoad_TrunkBaseRefuseDefaultOverrideAndBranch(t *testing.T) {
	cfg, _, err := Load(writeWorktreeRepo(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Trunk.BaseRefuseSet(), ","); got != "dirty,diverged,ahead,behind,offline,unresolved" {
		t.Errorf("default base_refuse = %q", got)
	}
	if got := strings.Join(cfg.Trunk.RefuseSet(), ","); got != "dirty,diverged" {
		t.Errorf("engage refuse default moved: %q", got)
	}
	if cfg.Trunk.Branch != "" {
		t.Errorf("default branch hint = %q, want none", cfg.Trunk.Branch)
	}

	cfg, _, err = Load(writeWorktreeRepo(t, "[trunk]\nbase_refuse = [\"dirty\"]\nbranch = \"main\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Trunk.BaseRefuses("dirty") || cfg.Trunk.BaseRefuses("ahead") || cfg.Trunk.BaseRefuses("unresolved") {
		t.Errorf("base_refuse override not honoured: %v", cfg.Trunk.BaseRefuseSet())
	}
	if cfg.Trunk.Branch != "main" || !cfg.Trunk.Refuses("diverged") {
		t.Errorf("branch = %q, refuse = %v", cfg.Trunk.Branch, cfg.Trunk.RefuseSet())
	}

	empty, _, err := Load(writeWorktreeRepo(t, "[trunk]\nbase_refuse = []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Trunk.BaseRefuseSet()) != 0 {
		t.Errorf("an explicit empty base_refuse must stop nothing: %v", empty.Trunk.BaseRefuseSet())
	}

	_, _, err = Load(writeWorktreeRepo(t, "[trunk]\nbase_refuse = [\"bogus\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "base_refuse") || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("Load error = %v, want one naming base_refuse and the state", err)
	}
}

func TestLoad_TrunkRefuseRejectsAnUnknownState(t *testing.T) {
	_, _, err := Load(writeWorktreeRepo(t, "[trunk]\nrefuse = [\"dirtyy\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "dirtyy") {
		t.Fatalf("Load error = %v, want one naming the unknown state", err)
	}
}
