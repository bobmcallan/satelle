package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// areaScopes parses `sync scopes` tabwriter output into area -> scope,
// ignoring indented "shared: <path>" detail lines.
func areaScopes(out string) map[string]string {
	scopes := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 {
			scopes[fields[0]] = fields[1]
		}
	}
	return scopes
}

// TestSyncScopesDefaultsToLocal: with no [sync] table, every area prints local
// — the safe default, nothing syncs without opt-in (AC1).
func TestSyncScopesDefaultsToLocal(t *testing.T) {
	tempRepo(t)
	out, err := runRoot(t, "sync", "scopes")
	if err != nil {
		t.Fatalf("sync scopes: %v\n%s", err, out)
	}
	scopes := areaScopes(out)
	for _, area := range []string{"documents", "workflows", "principles", "skills", "constitution", "agents", "tasks", "stories", "ledger", "executions"} {
		if scopes[area] != "local" {
			t.Errorf("area %q scope = %q, want local (full output:\n%s)", area, scopes[area], out)
		}
	}
}

// TestSyncScopesConfiguredAndOverlay exercises AC1 (committed [sync] + a
// satelle.local.toml per-dev override) end to end via the CLI, and AC4 (the
// command is the resolver's production caller).
func TestSyncScopesConfiguredAndOverlay(t *testing.T) {
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	committed := "web_port = 8181\n\n[sync]\nskills = \"shared\"\ndocuments = \"personal\"\n"
	if err := os.WriteFile(cfgPath, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	local := "[sync]\ndocuments = \"local\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.local.toml"), []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "sync", "scopes")
	if err != nil {
		t.Fatalf("sync scopes: %v\n%s", err, out)
	}
	scopes := areaScopes(out)
	if scopes["skills"] != "shared" {
		t.Errorf("skills scope = %q, want shared (committed value, no overlay)", scopes["skills"])
	}
	if scopes["documents"] != "local" {
		t.Errorf("documents scope = %q, want local (overlay override of committed personal)", scopes["documents"])
	}
}

// TestSyncScopesInvalidScopeRefuses: an area explicitly set to a value outside
// local|personal|shared refuses the command rather than silently coercing to
// local (plan addendum #2).
func TestSyncScopesInvalidScopeRefuses(t *testing.T) {
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	committed := "[sync]\ntasks = \"sometimes\"\n"
	if err := os.WriteFile(cfgPath, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runRoot(t, "sync", "scopes"); err == nil {
		t.Error("sync scopes with an invalid configured scope did not error")
	}
}

// TestSyncScopesListsSharedFiles: a file with `shared: true` frontmatter inside
// a personal-scope area is listed by the command (AC2, AC4).
func TestSyncScopesListsSharedFiles(t *testing.T) {
	repo := tempRepo(t)
	cfgPath := filepath.Join(repo, ".satelle", "satelle.toml")
	if err := os.WriteFile(cfgPath, []byte("[sync]\nskills = \"personal\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(repo, ".satelle", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shared := "---\ntype: skill\nshared: true\n---\nbody\n"
	unshared := "---\ntype: skill\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "shared-one.md"), []byte(shared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "private-one.md"), []byte(unshared), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runRoot(t, "sync", "scopes")
	if err != nil {
		t.Fatalf("sync scopes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "shared-one.md") {
		t.Errorf("sync scopes should list shared-one.md as shared:\n%s", out)
	}
	if strings.Contains(out, "private-one.md") {
		t.Errorf("sync scopes should NOT list private-one.md:\n%s", out)
	}
}
