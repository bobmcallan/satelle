package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes a file under repo, creating parent dirs.
func writeFile(t *testing.T, repo, rel, body string) {
	t.Helper()
	dest := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findConfigFile(files []ConfigFile, path string) (ConfigFile, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return ConfigFile{}, false
}

// TestConfigAreasExcludesDocumentsAndWorkState: the candidate set is the five
// authored areas + tasks, never documents (its own kind) or the work-state areas.
func TestConfigAreasExcludesDocumentsAndWorkState(t *testing.T) {
	want := map[string]bool{"workflows": true, "principles": true, "skills": true, "constitution": true, "agents": true, "tasks": true}
	seen := map[string]bool{}
	for _, a := range ConfigAreas {
		seen[a] = true
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("ConfigAreas missing %q", k)
		}
	}
	for _, bad := range []string{"documents", "stories", "ledger", "executions"} {
		if seen[bad] {
			t.Errorf("ConfigAreas must not include %q", bad)
		}
	}
}

// TestConfigFilesSkipsLocalArea: a local-scope area (the default, and an
// explicit local) contributes nothing — AC1 skip scope=local.
func TestConfigFilesSkipsLocalArea(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/skills/a.md", "body")
	// No [sync] table -> every area local -> nothing to push.
	files, err := ConfigFiles(Config{}, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("local-scope walk returned %d files, want 0: %+v", len(files), files)
	}
	// Explicit local behaves the same.
	files, err = ConfigFiles(Config{Sync: map[string]string{"skills": "local"}}, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("explicit local walk returned %d files, want 0", len(files))
	}
}

// TestConfigFilesTierResolution: a shared-scope area is SharedTier wholesale;
// a personal-scope area is PersonalTier per file, PROMOTED to SharedTier when
// the file is frontmarked shared (AC1 honors the per-file shared flag).
func TestConfigFilesTierResolution(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/skills/personal-one.md", "---\ntype: skill\n---\nbody\n")
	writeFile(t, repo, ".satelle/skills/shared-one.md", "---\ntype: skill\nshared: true\n---\nbody\n")
	writeFile(t, repo, ".satelle/principles/team-rule.md", "---\ntype: principle\n---\nbody\n")
	writeFile(t, repo, ".satelle/agents.toml", "[executor]\nharness = \"in-loop\"\n")

	cfg := Config{Sync: map[string]string{"skills": "personal", "principles": "shared", "agents": "personal"}}
	files, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	// skills/personal-one.md -> PersonalTier (personal area, no shared flag).
	if f, ok := findConfigFile(files, "skills/personal-one.md"); !ok {
		t.Error("missing skills/personal-one.md")
	} else if f.Tier != PersonalTier {
		t.Errorf("personal-one tier = %v, want PersonalTier", f.Tier)
	}
	// skills/shared-one.md -> SharedTier (personal area, shared flag promotes it).
	if f, ok := findConfigFile(files, "skills/shared-one.md"); !ok {
		t.Error("missing skills/shared-one.md")
	} else if f.Tier != SharedTier {
		t.Errorf("shared-one tier = %v, want SharedTier", f.Tier)
	}
	// principles/team-rule.md -> SharedTier (shared area wholesale).
	if f, ok := findConfigFile(files, "principles/team-rule.md"); !ok {
		t.Error("missing principles/team-rule.md")
	} else if f.Tier != SharedTier {
		t.Errorf("team-rule tier = %v, want SharedTier", f.Tier)
	}
	// agents.toml -> PersonalTier (personal area, TOML has no frontmatter).
	if f, ok := findConfigFile(files, "agents.toml"); !ok {
		t.Error("missing agents.toml")
	} else if f.Tier != PersonalTier {
		t.Errorf("agents.toml tier = %v, want PersonalTier", f.Tier)
	}
}

// TestConfigFilesExcludesReservedViews: the generated index.md/log.md and a
// README are never part of a push (they are not authored substrate).
func TestConfigFilesExcludesReservedViews(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/skills/keep.md", "body")
	writeFile(t, repo, ".satelle/skills/index.md", "generated")
	writeFile(t, repo, ".satelle/skills/log.md", "generated")
	writeFile(t, repo, ".satelle/skills/README.md", "readme")
	cfg := Config{Sync: map[string]string{"skills": "shared"}}
	files, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if _, ok := findConfigFile(files, "skills/keep.md"); !ok {
		t.Error("keep.md should be included")
	}
	for _, drop := range []string{"skills/index.md", "skills/log.md", "skills/README.md"} {
		if f, ok := findConfigFile(files, drop); ok {
			t.Errorf("reserved view %q should be excluded: %+v", drop, f)
		}
	}
}

// TestConfigFilesServerPathStableUnderSubstrateRoots: when [substrate_roots]
// relocates a kind outside .satelle/, the server path is still the stable
// area/relpath key (not the absolute on-disk path) — so a deploy restores to a
// fresh repo identically.
func TestConfigFilesServerPathStableUnderSubstrateRoots(t *testing.T) {
	repo := t.TempDir()
	alt := filepath.Join(repo, "elsewhere")
	writeFile(t, repo, "elsewhere/skills/relocated.md", "body")
	cfg := Config{Sync: map[string]string{"skills": "shared"}, SubstrateRoots: map[string]string{"skills": alt}}
	files, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if f, ok := findConfigFile(files, "skills/relocated.md"); !ok {
		t.Errorf("relocated skill missing; files=%+v", files)
	} else if string(f.Content) != "body" {
		t.Errorf("content = %q", f.Content)
	}
}

// TestConfigFilesSortedDeterministically: the walk output is sorted by Path.
func TestConfigFilesSortedDeterministically(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/skills/zeta.md", "z")
	writeFile(t, repo, ".satelle/skills/alpha.md", "a")
	writeFile(t, repo, ".satelle/principles/mid.md", "m")
	cfg := Config{Sync: map[string]string{"skills": "shared", "principles": "shared"}}
	files, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	for i := 1; i < len(files); i++ {
		if files[i-1].Path > files[i].Path {
			t.Errorf("not sorted: %q before %q", files[i-1].Path, files[i].Path)
		}
	}
}

// TestConfigFilesInvalidScopeErrors: an explicitly invalid scope refuses rather
// than silently coercing to local (AC1 fail-fast).
func TestConfigFilesInvalidScopeErrors(t *testing.T) {
	repo := t.TempDir()
	cfg := Config{Sync: map[string]string{"skills": "bogus"}}
	if _, err := ConfigFiles(cfg, repo); err == nil {
		t.Error("ConfigFiles with an invalid scope did not error")
	}
}
