package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The repo agents layer moved to workflows/agents.toml (sty_10f732ed). These
// cover the resolution rule the move rests on: canonical wins, legacy still
// loads, and neither present names the canonical location.

func writeAgents(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAgentsPathPrefersCanonical(t *testing.T) {
	dataDir := t.TempDir()
	canonical := filepath.Join(dataDir, AgentsConfigDir, AgentsConfigName)
	writeAgents(t, canonical, "[executor]\ncommand = \"in-loop\"\n")

	got, legacy := AgentsPath(dataDir)
	if got != canonical || legacy {
		t.Fatalf("AgentsPath = (%q, %v), want (%q, false)", got, legacy, canonical)
	}
}

func TestAgentsPathFallsBackToLegacy(t *testing.T) {
	dataDir := t.TempDir()
	old := filepath.Join(dataDir, AgentsConfigName)
	writeAgents(t, old, "[executor]\ncommand = \"in-loop\"\n")

	got, legacy := AgentsPath(dataDir)
	if got != old || !legacy {
		t.Fatalf("AgentsPath = (%q, %v), want (%q, true)", got, legacy, old)
	}
}

// An unconverted repo must keep RUNNING, not just resolve: LoadAgents is the
// path every consumer reaches the layer through.
func TestLoadAgentsReadsEitherLocation(t *testing.T) {
	for _, tc := range []struct{ name, rel string }{
		{"canonical", AgentsConfigDir + "/" + AgentsConfigName},
		{"legacy", AgentsConfigName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			writeAgents(t, filepath.Join(dataDir, filepath.FromSlash(tc.rel)),
				"[reviewer]\nmodel = \"from-"+tc.name+"\"\n")
			ac, err := LoadAgents(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			if got := ac.Reviewer.Model; got != "from-"+tc.name {
				t.Fatalf("reviewer model = %q, want the %s file's value", got, tc.name)
			}
		})
	}
}

// Both present is a converting repo mid-flight (init relocates, but a stale copy
// may linger): the canonical file must win rather than the read depending on
// which one os.Stat reached first.
func TestAgentsPathCanonicalWinsWhenBothExist(t *testing.T) {
	dataDir := t.TempDir()
	writeAgents(t, filepath.Join(dataDir, AgentsConfigDir, AgentsConfigName), "[reviewer]\nmodel = \"canonical\"\n")
	writeAgents(t, filepath.Join(dataDir, AgentsConfigName), "[reviewer]\nmodel = \"legacy\"\n")

	ac, err := LoadAgents(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if ac.Reviewer.Model != "canonical" {
		t.Fatalf("reviewer model = %q, want the canonical file to win", ac.Reviewer.Model)
	}
}

// Neither present: the returned path names where the file BELONGS, so a
// "missing agents.toml" refusal points forward rather than at the old home.
func TestAgentsPathNamesCanonicalWhenAbsent(t *testing.T) {
	dataDir := t.TempDir()
	got, legacy := AgentsPath(dataDir)
	want := filepath.Join(dataDir, AgentsConfigDir, AgentsConfigName)
	if got != want || legacy {
		t.Fatalf("AgentsPath = (%q, %v), want (%q, false)", got, legacy, want)
	}
}

// AC4: the machine-wide catalog is a DIFFERENT file and does not move. A repo
// binding at either location must still inherit from ~/.satelle/agents.toml.
func TestProfileInheritanceWorksFromBothLocations(t *testing.T) {
	catalog := `[profiles.shared]
command = "claude -p"
model = "from-catalog"
`
	for _, tc := range []struct{ name, rel string }{
		{"canonical", AgentsConfigDir + "/" + AgentsConfigName},
		{"legacy", AgentsConfigName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			writeAgents(t, filepath.Join(dataDir, filepath.FromSlash(tc.rel)),
				"[reviewer]\nprofile = \"shared\"\n")
			home := t.TempDir()
			catalogPath := filepath.Join(home, GlobalAgentsName)
			writeAgents(t, catalogPath, catalog)

			gc, err := loadGlobalAgentsFile(catalogPath)
			if err != nil {
				t.Fatal(err)
			}
			repo, err := LoadAgents(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			resolved, _, err := ResolveAgents(repo, gc)
			if err != nil {
				t.Fatal(err)
			}
			if got := resolved.Reviewer.Model; got != "from-catalog" {
				t.Fatalf("model = %q, want the catalog's value to be inherited", got)
			}
		})
	}
}
