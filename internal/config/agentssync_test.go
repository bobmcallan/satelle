package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// The agents layer now sits INSIDE the workflows dir but keeps its own sync area
// (sty_10f732ed). That combination has two failure modes, and these pin both.

// Trap 1 — the double push. The workflows area is directory-shaped and walks
// every file with no extension filter, so without an explicit skip the agents
// file would be pushed a second time under a workflows/ key.
func TestAgentsFilePushedExactlyOnce(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/workflows/agents.toml", "[executor]\ncommand = \"in-loop\"\n")
	writeFile(t, repo, ".satelle/workflows/done.md", "---\nname: done\ntype: workflow\nscope: project\n---\n\n## *\n- raised\n")

	cfg := Config{Sync: map[string]string{"workflows": "shared", "agents": "personal"}}
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, f := range b.Files {
		if strings.Contains(f.Path, AgentsConfigName) {
			keys = append(keys, f.Path)
		}
	}
	if len(keys) != 1 {
		t.Fatalf("agents file must appear exactly once in the bundle, got %v", keys)
	}
	// Trap 2 — the deploy round-trip. subsync writes each key back as
	// filepath.Join(dataDir, key), so a bare "agents.toml" would land every pull
	// at the LEGACY path forever.
	if keys[0] != AgentsRel {
		t.Fatalf("agents server key = %q, want %q so a deploy lands at the canonical path", keys[0], AgentsRel)
	}
}

// A repo still on the legacy path pushes under the CANONICAL key, so deploying
// its own bundle converges it rather than pinning it to the old location.
func TestLegacyPathRepoPushesUnderCanonicalKey(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/agents.toml", "[executor]\ncommand = \"in-loop\"\n")

	cfg := Config{Sync: map[string]string{"agents": "personal"}}
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findConfigFile(b.Files, AgentsRel); !ok {
		var got []string
		for _, f := range b.Files {
			got = append(got, f.Path)
		}
		t.Fatalf("legacy-path repo must still push under %q, got %v", AgentsRel, got)
	}
}

// The area's on-disk location follows the same fallback the loader uses, so a
// push from an unconverted repo reads the file that is actually there.
func TestConfigAreaLocationFollowsFallback(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/agents.toml", "[executor]\n")
	cfg := Config{}
	loc, isDir := ConfigAreaLocation(cfg, repo, "agents")
	if isDir {
		t.Fatal("the agents area is file-shaped")
	}
	if want := filepath.Join(repo, ".satelle", AgentsConfigName); loc != want {
		t.Fatalf("location = %q, want the legacy file that exists (%q)", loc, want)
	}
}

// TestConfigFilesNeverIncludesMachineCatalog pins AC3 of sty_940938e3: even
// with blanket personal sync, the push set is built only from the repo tree —
// the machine catalog (~/.satelle/agents.toml) is structurally unreachable.
func TestConfigFilesNeverIncludesMachineCatalog(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/workflows/agents.toml", "[executor]\ncommand = \"in-loop\"\n")
	writeFile(t, repo, ".satelle/satelle.toml", "")
	// A machine catalog path must never appear even if the home has one — ConfigFiles
	// walks repoRoot only.
	cfg := Config{Sync: map[string]string{"all": "personal"}}
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range b.Files {
		if filepath.IsAbs(f.Path) {
			t.Errorf("bundle path must be repo-relative, got absolute %q", f.Path)
		}
		if strings.HasPrefix(f.Path, string(filepath.Separator)) {
			t.Errorf("bundle path must not be root-absolute: %q", f.Path)
		}
		// Never the bare machine-catalog basename as a top-level personal home key
		// outside the workflows/agents.toml repo layout.
		if f.Path == "agents.toml" {
			t.Errorf("machine-catalog bare key %q must not appear — repo agents is %q", f.Path, AgentsRel)
		}
		if strings.Contains(f.Path, string(filepath.Separator)+".."+string(filepath.Separator)) {
			t.Errorf("bundle path escapes repo: %q", f.Path)
		}
	}
	// Repo agents still ships under its workflows-relative key when agents/workflows sync.
	cfg2 := Config{Sync: map[string]string{"agents": "personal", "workflows": "personal"}}
	b2, err := ConfigFiles(cfg2, repo)
	if err != nil {
		t.Fatal(err)
	}
	foundRepoAgents := false
	for _, f := range b2.Files {
		if f.Path == AgentsRel {
			foundRepoAgents = true
		}
		if f.Path == GlobalAgentsName && f.Path != AgentsRel {
			t.Errorf("unexpected bare catalog key %q", f.Path)
		}
	}
	if !foundRepoAgents {
		t.Fatalf("expected repo agents at %q in personal agents/workflows sync", AgentsRel)
	}
}
