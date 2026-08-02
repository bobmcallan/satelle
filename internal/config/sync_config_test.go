package config

import (
	"os"
	"path/filepath"
	"strings"
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

// TestDocumentFilesScopeLocalSkipped: a local-scope documents area contributes
// nothing and reports LocalScope so the CLI can print a clear skip message.
func TestDocumentFilesScopeLocalSkipped(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/documents/note.md", "body")
	b, scope, err := DocumentFiles(Config{}, repo)
	if err != nil {
		t.Fatalf("DocumentFiles: %v", err)
	}
	if scope != LocalScope {
		t.Errorf("scope = %v, want LocalScope", scope)
	}
	if len(b.Files) != 0 {
		t.Errorf("local documents walk returned %d files, want 0", len(b.Files))
	}
}

// TestDocumentFilesSharedFlagPromotes: inside a personal-scope documents area,
// shared:true promotes a file to SharedTier; others stay PersonalTier.
func TestDocumentFilesSharedFlagPromotes(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/documents/private.md", "---\ntype: document\n---\npriv\n")
	writeFile(t, repo, ".satelle/documents/shared.md", "---\ntype: document\nshared: true\n---\nteam\n")
	cfg := Config{Sync: map[string]string{"documents": "personal"}}
	b, scope, err := DocumentFiles(cfg, repo)
	if err != nil {
		t.Fatalf("DocumentFiles: %v", err)
	}
	if scope != PersonalScope {
		t.Errorf("scope = %v, want PersonalScope", scope)
	}
	if f, ok := findConfigFile(b.Files, "documents/private.md"); !ok {
		t.Error("missing documents/private.md")
	} else if f.Tier != PersonalTier {
		t.Errorf("private tier = %v, want PersonalTier", f.Tier)
	}
	if f, ok := findConfigFile(b.Files, "documents/shared.md"); !ok {
		t.Error("missing documents/shared.md")
	} else if f.Tier != SharedTier {
		t.Errorf("shared tier = %v, want SharedTier", f.Tier)
	}
}

// TestConfigAreasExcludesDocumentsAndWorkState: the candidate set is the authored
// config areas + tasks + settings (satelle.toml), never documents (its own kind)
// or the work-state areas.
func TestConfigAreasExcludesDocumentsAndWorkState(t *testing.T) {
	want := map[string]bool{"workflows": true, "principles": true, "skills": true, "constitution": true, "agents": true, "tasks": true, "settings": true}
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
	b, err := ConfigFiles(Config{}, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if len(b.Files) != 0 {
		t.Errorf("local-scope walk returned %d files, want 0: %+v", len(b.Files), b.Files)
	}
	// Explicit local behaves the same.
	b, err = ConfigFiles(Config{Sync: map[string]string{"skills": "local"}}, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if len(b.Files) != 0 {
		t.Errorf("explicit local walk returned %d files, want 0", len(b.Files))
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
	writeFile(t, repo, ".satelle/workflows/agents.toml", "[executor]\nharness = \"in-loop\"\n")

	cfg := Config{Sync: map[string]string{"skills": "personal", "principles": "shared", "agents": "personal"}}
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	// skills/personal-one.md -> PersonalTier (personal area, no shared flag).
	if f, ok := findConfigFile(b.Files, "skills/personal-one.md"); !ok {
		t.Error("missing skills/personal-one.md")
	} else if f.Tier != PersonalTier {
		t.Errorf("personal-one tier = %v, want PersonalTier", f.Tier)
	}
	// skills/shared-one.md -> SharedTier (personal area, shared flag promotes it).
	if f, ok := findConfigFile(b.Files, "skills/shared-one.md"); !ok {
		t.Error("missing skills/shared-one.md")
	} else if f.Tier != SharedTier {
		t.Errorf("shared-one tier = %v, want SharedTier", f.Tier)
	}
	// principles/team-rule.md -> SharedTier (shared area wholesale).
	if f, ok := findConfigFile(b.Files, "principles/team-rule.md"); !ok {
		t.Error("missing principles/team-rule.md")
	} else if f.Tier != SharedTier {
		t.Errorf("team-rule tier = %v, want SharedTier", f.Tier)
	}
	// agents.toml -> PersonalTier (personal area, TOML has no frontmatter).
	if f, ok := findConfigFile(b.Files, "workflows/agents.toml"); !ok {
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
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if _, ok := findConfigFile(b.Files, "skills/keep.md"); !ok {
		t.Error("keep.md should be included")
	}
	for _, drop := range []string{"skills/index.md", "skills/log.md", "skills/README.md"} {
		if f, ok := findConfigFile(b.Files, drop); ok {
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
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if f, ok := findConfigFile(b.Files, "skills/relocated.md"); !ok {
		t.Errorf("relocated skill missing; files=%+v", b.Files)
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
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	for i := 1; i < len(b.Files); i++ {
		if b.Files[i-1].Path > b.Files[i].Path {
			t.Errorf("not sorted: %q before %q", b.Files[i-1].Path, b.Files[i].Path)
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

// TestLocalOnlyPath: the .local exclusion matcher — positives and D2 negatives.
func TestLocalOnlyPath(t *testing.T) {
	yes := []string{
		"satelle.local.toml",
		"skills/my-thing.local.md",
		"documents/notes.LOCAL.md",
		"x.local",
		".local",
		"secrets.local/keys.md",
		"x.local.yaml",
	}
	for _, p := range yes {
		if !LocalOnlyPath(p) {
			t.Errorf("LocalOnlyPath(%q) = false, want true", p)
		}
	}
	no := []string{
		"skills/local/x.md",
		"local.md",
		"mylocal.md",
		"locale.md",
		"skills/keep.md",
		"agents.toml",
		"constitution.md",
	}
	for _, p := range no {
		if LocalOnlyPath(p) {
			t.Errorf("LocalOnlyPath(%q) = true, want false", p)
		}
	}
}

// TestAssembleWithholdsLocalFiles: assemble is the sole exclusion seam.
func TestAssembleWithholdsLocalFiles(t *testing.T) {
	in := []ConfigFile{
		{Path: "skills/keep.md", Content: []byte("ok")},
		{Path: "skills/secret.local.md", Content: []byte("secret")},
		{Path: "notes.local.md", Content: []byte("x")},
	}
	b := assemble(in)
	if len(b.Files) != 1 || b.Files[0].Path != "skills/keep.md" {
		t.Fatalf("Files = %+v, want only skills/keep.md", b.Files)
	}
	if len(b.Withheld) != 2 {
		t.Fatalf("Withheld = %v, want 2", b.Withheld)
	}
	// Withheld is sorted.
	if b.Withheld[0] != "notes.local.md" || b.Withheld[1] != "skills/secret.local.md" {
		t.Errorf("Withheld not sorted: %v", b.Withheld)
	}
}

// TestSkillsAreaWithholdsLocalFile: the case that is NOT protected today —
// a .local file inside an opted-in authored area.
func TestSkillsAreaWithholdsLocalFile(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/skills/keep.md", "body")
	writeFile(t, repo, ".satelle/skills/my-thing.local.md", "SECRET")
	cfg := Config{Sync: map[string]string{"skills": "personal"}}
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if _, ok := findConfigFile(b.Files, "skills/keep.md"); !ok {
		t.Error("keep.md missing from Files")
	}
	if _, ok := findConfigFile(b.Files, "skills/my-thing.local.md"); ok {
		t.Error("my-thing.local.md must not be in Files")
	}
	found := false
	for _, w := range b.Withheld {
		if w == "skills/my-thing.local.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("Withheld missing skills/my-thing.local.md: %v", b.Withheld)
	}
}

// TestEveryConfigAreaWithholdsLocal: table derived from ConfigAreas + documents
// so a new area added later extends the guarantee automatically (AC2).
func TestEveryConfigAreaWithholdsLocal(t *testing.T) {
	repo := t.TempDir()
	areas := append(append([]string{}, ConfigAreas...), "documents")
	cfgSync := map[string]string{}
	for _, area := range areas {
		cfgSync[area] = "personal"
		loc, isDir := ConfigAreaLocation(Config{}, repo, area)
		if loc == "" {
			// plant under default .satelle layout
			if area == "constitution" {
				writeFile(t, repo, ".satelle/constitution.md", "ok")
				writeFile(t, repo, ".satelle/constitution.local.md", "secret")
				continue
			}
			if area == "agents" {
				writeFile(t, repo, ".satelle/workflows/agents.toml", "[x]\n")
				// single-file area: LocalOnlyPath on agents.local.toml-shaped path
				// is exercised by the basename of a would-be single-file area;
				// plant a dir file for directory areas only.
				continue
			}
		}
		_ = loc
		_ = isDir
		if area == "constitution" {
			writeFile(t, repo, ".satelle/constitution.md", "ok")
			// single-file areas: LocalOnlyPath matches only if the server path
			// itself is local — constitution.md is not. The per-area guarantee
			// for single-file areas is exercised when the file path matches.
			continue
		}
		if area == "agents" {
			writeFile(t, repo, ".satelle/workflows/agents.toml", "[x]\n")
			continue
		}
		if area == "settings" {
			writeFile(t, repo, ".satelle/satelle.toml", "[sync]\nall = \"personal\"\n")
			continue
		}
		// directory areas
		writeFile(t, repo, ".satelle/"+area+"/thing.md", "ok")
		writeFile(t, repo, ".satelle/"+area+"/thing.local.md", "secret")
	}
	// Config areas
	b, err := ConfigFiles(Config{Sync: cfgSync}, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	for _, area := range ConfigAreas {
		if area == "constitution" || area == "agents" || area == "settings" {
			continue // single-file; no thing.local.md path
		}
		keep := area + "/thing.md"
		secret := area + "/thing.local.md"
		if _, ok := findConfigFile(b.Files, keep); !ok {
			t.Errorf("%s missing from Files", keep)
		}
		if _, ok := findConfigFile(b.Files, secret); ok {
			t.Errorf("%s must not be in Files", secret)
		}
		found := false
		for _, w := range b.Withheld {
			if w == secret {
				found = true
			}
		}
		if !found {
			t.Errorf("Withheld missing %s: %v", secret, b.Withheld)
		}
	}
	// documents area
	db, _, err := DocumentFiles(Config{Sync: cfgSync}, repo)
	if err != nil {
		t.Fatalf("DocumentFiles: %v", err)
	}
	if _, ok := findConfigFile(db.Files, "documents/thing.md"); !ok {
		t.Error("documents/thing.md missing")
	}
	if _, ok := findConfigFile(db.Files, "documents/thing.local.md"); ok {
		t.Error("documents/thing.local.md must not be in Files")
	}
	found := false
	for _, w := range db.Withheld {
		if w == "documents/thing.local.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("documents Withheld missing thing.local.md: %v", db.Withheld)
	}
}

// TestLocalExclusionIgnoresConfig: LocalOnlyPath takes no Config — withholding
// holds under every scope posture and under shared:true frontmatter (AC4).
func TestLocalExclusionIgnoresConfig(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/skills/keep.md", "ok")
	writeFile(t, repo, ".satelle/skills/secret.local.md", "---\nshared: true\n---\nSECRET\n")

	cases := []Config{
		{},
		{Sync: map[string]string{"all": "personal"}},
		{Sync: map[string]string{"all": "shared"}},
		{Sync: map[string]string{"skills": "shared"}},
		{Sync: map[string]string{"skills": "personal"}},
	}
	for i, cfg := range cases {
		b, err := ConfigFiles(cfg, repo)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		// Zero config: local scope → nothing walked; still no secret in Files.
		if _, ok := findConfigFile(b.Files, "skills/secret.local.md"); ok {
			t.Errorf("case %d: secret.local.md in Files under config %+v", i, cfg.Sync)
		}
		if len(cfg.Sync) == 0 {
			continue // local scope — Withheld is empty because walk never saw it
		}
		found := false
		for _, w := range b.Withheld {
			if w == "skills/secret.local.md" {
				found = true
			}
		}
		if !found {
			t.Errorf("case %d: Withheld missing secret.local.md: %v", i, b.Withheld)
		}
	}
}

// TestSettingsBundleExcludesLocalOverlay (sty_ea18294f AC2): satelle.toml joins
// the bundle; satelle.local.toml never rides, even with a sentinel secret.
func TestSettingsBundleExcludesLocalOverlay(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, repo, ".satelle/satelle.toml", "[sync]\nall = \"personal\"\n[hosted]\nproject = \"probe\"\n")
	const secret = "SUPER_SECRET_TOKEN_XYZ"
	writeFile(t, repo, ".satelle/satelle.local.toml", "[vars]\nAPI_KEY = \""+secret+"\"\n")
	cfg := Config{Sync: map[string]string{"all": "personal"}}
	b, err := ConfigFiles(cfg, repo)
	if err != nil {
		t.Fatalf("ConfigFiles: %v", err)
	}
	if _, ok := findConfigFile(b.Files, "satelle.toml"); !ok {
		t.Error("satelle.toml missing from Files")
	}
	for _, f := range b.Files {
		if f.Path == "satelle.local.toml" {
			t.Error("satelle.local.toml must not be in Files")
		}
		if strings.Contains(string(f.Content), secret) {
			t.Errorf("secret leaked in %s content", f.Path)
		}
		// [hosted] project must be redacted at push
		if f.Path == "satelle.toml" && strings.Contains(string(f.Content), `project = "probe"`) {
			t.Error("satelle.toml content still carries [hosted] project after redact")
		}
	}
	// Overlay may be listed as withheld only if walked — it is not in any area location.
	for _, w := range b.Withheld {
		if w == "satelle.local.toml" {
			// fine if reported; not required
		}
	}
}

// TestLocalOnlyPathSettingsOverlay pins the .local rule for the overlay path.
func TestLocalOnlyPathSettingsOverlay(t *testing.T) {
	if !LocalOnlyPath("satelle.local.toml") {
		t.Error("LocalOnlyPath(satelle.local.toml) = false, want true")
	}
}

// TestRedactForTransmitStripsHostedProject: only settings area is redacted.
func TestRedactForTransmitStripsHostedProject(t *testing.T) {
	body := []byte("[sync]\nall = \"personal\"\n[hosted]\nproject = \"alpha\"\nserver = \"https://x\"\n")
	got := string(redactForTransmit("settings", body))
	if strings.Contains(got, `project = "alpha"`) {
		t.Errorf("project not stripped: %s", got)
	}
	if !strings.Contains(got, `all = "personal"`) {
		t.Errorf("sync lost: %s", got)
	}
	// other areas unchanged
	other := redactForTransmit("skills", body)
	if string(other) != string(body) {
		t.Error("non-settings area was redacted")
	}
}
