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

func TestLoad_TrunkRefuseRejectsAnUnknownState(t *testing.T) {
	_, _, err := Load(writeWorktreeRepo(t, "[trunk]\nrefuse = [\"dirtyy\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "dirtyy") {
		t.Fatalf("Load error = %v, want one naming the unknown state", err)
	}
}
