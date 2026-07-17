package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
)

// TestMigrateConvergesLegacyFixture (sty_a3915840 AC5): a full legacy layout
// dry-runs a complete plan, --yes converges, and a second run is a no-op.
func TestMigrateConvergesLegacyFixture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	// Minimal agents so validateDeployment can pass.
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.toml"), []byte("[executor]\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte("web_port = 8181\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Legacy runtime under data_dir.
	db, err := store.Open(filepath.Join(dataDir, config.DefaultDBName))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "logs", "x.log"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "stories"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.toml.bak"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stale managed gitignore (pre-relocation form).
	staleGI := `# >>> satelle (managed) >>>
.satelle/satelle.db
.satelle/satelle.db-wal
.satelle/logs/
.satelle/backups/
.satelle/stories/
# <<< satelle (managed) <<<
`
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(staleGI), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unedited embedded seed so prune runs and backups must land home-keyed.
	var seedRel string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "skills" {
			continue
		}
		seedRel = d.Kind + "/" + d.Name + ".md"
		if err := os.MkdirAll(filepath.Join(dataDir, d.Kind), 0o755); err != nil {
			t.Fatal(err)
		}
		// Write body identical to embedded (unedited seed).
		if err := os.WriteFile(filepath.Join(dataDir, filepath.FromSlash(seedRel)), []byte(d.Body), 0o644); err != nil {
			t.Fatal(err)
		}
		break
	}

	// RuntimeDir deliberately set to the LEGACY dataDir (the bug path): migrate
	// must re-resolve to home-keyed after relocate before prune backups.
	a := &app.App{
		Config:     config.Config{},
		RepoRoot:   repo,
		DataDir:    dataDir,
		RuntimeDir: dataDir,
	}

	// Dry-run: nothing applied.
	var dry strings.Builder
	if err := runMigrate(&dry, a, false); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, dry.String())
	}
	s := dry.String()
	if !strings.Contains(s, "dry-run only") {
		t.Errorf("expected dry-run notice:\n%s", s)
	}
	if !strings.Contains(s, "runtime relocate") {
		t.Errorf("expected runtime relocate in plan:\n%s", s)
	}
	if !strings.Contains(s, "logs/") {
		t.Errorf("expected logs residue in plan:\n%s", s)
	}
	if !strings.Contains(s, "gitignore") {
		t.Errorf("expected gitignore step:\n%s", s)
	}
	// Legacy DB still present after dry-run.
	if !fileExists(filepath.Join(dataDir, config.DefaultDBName)) {
		t.Fatal("dry-run must not move the legacy db")
	}
	homeDB := filepath.Join(home, config.RepoKey(repo), config.DefaultDBName)
	if fileExists(homeDB) {
		t.Fatal("dry-run must not create home-keyed db")
	}

	// Apply.
	var apply strings.Builder
	if err := runMigrate(&apply, a, true); err != nil {
		t.Fatalf("apply: %v\n%s", err, apply.String())
	}
	if !fileExists(homeDB) {
		t.Fatalf("home-keyed db missing after migrate: %s\n%s", homeDB, apply.String())
	}
	if fileExists(filepath.Join(dataDir, config.DefaultDBName)) {
		t.Fatal("legacy satelle.db should be removed as residue")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "logs")); err == nil {
		t.Fatal("legacy logs/ should be removed")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "agents.toml.bak")); err == nil {
		t.Fatal("agents.toml.bak should be removed")
	}
	gi, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if strings.Contains(string(gi), ".satelle/satelle.db") {
		t.Errorf("gitignore still ignores .satelle/satelle.db:\n%s", gi)
	}
	if !strings.Contains(string(gi), ".satelle/satelle.local.toml") {
		t.Errorf("gitignore missing current local.toml entry:\n%s", gi)
	}
	if !strings.Contains(apply.String(), "migrate complete") {
		t.Errorf("expected complete line:\n%s", apply.String())
	}
	// Prune backups must land under home-keyed runtime, not re-create .satelle/backups/.
	if seedRel != "" {
		if _, err := os.Stat(filepath.Join(dataDir, "backups")); err == nil {
			t.Fatal("prune must not re-create .satelle/backups/ under dataDir")
		}
	}

	// Idempotent second run.
	var again strings.Builder
	if err := runMigrate(&again, a, true); err != nil {
		t.Fatalf("second run: %v\n%s", err, again.String())
	}
	if !strings.Contains(again.String(), "already on current structure") {
		t.Errorf("second run should be no-op:\n%s", again.String())
	}
}

func TestConvergeGitignoreRewritesManagedBlock(t *testing.T) {
	// sty_87c8a69c: content between markers becomes current; outside preserved.
	in := "node_modules/\n# >>> satelle (managed) >>>\n.satelle/satelle.db\n# <<< satelle (managed) <<<\n# keep me\n"
	out, changed := convergeGitignore(in)
	if !changed {
		t.Fatal("expected change")
	}
	if !strings.Contains(out, "node_modules/") {
		t.Error("lost content before markers")
	}
	if !strings.Contains(out, "# keep me") {
		t.Error("lost content after markers")
	}
	if strings.Contains(out, ".satelle/satelle.db") {
		t.Error("stale .satelle/satelle.db ignore should be gone")
	}
	if !strings.Contains(out, ".satelle/satelle.local.toml") {
		t.Error("current block missing local.toml")
	}
	// Already current → no change.
	_, changed2 := convergeGitignore(out)
	if changed2 {
		t.Error("converged body should be stable")
	}
}

func TestEnsureGitignoreConvergesStaleBlock(t *testing.T) {
	repo := t.TempDir()
	giPath := filepath.Join(repo, ".gitignore")
	stale := "foo/\n# >>> satelle (managed) >>>\n.satelle/logs/\n# <<< satelle (managed) <<<\nbar/\n"
	if err := os.WriteFile(giPath, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ensureGitignore(repo)
	if err != nil || !changed {
		t.Fatalf("converge: changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(giPath)
	s := string(got)
	if !strings.Contains(s, "foo/") || !strings.Contains(s, "bar/") {
		t.Errorf("outside content lost:\n%s", s)
	}
	if strings.Contains(s, ".satelle/logs/") {
		t.Errorf("stale logs entry remains:\n%s", s)
	}
	changed2, _ := ensureGitignore(repo)
	if changed2 {
		t.Error("second ensure should be no-op")
	}
}

// TestMigrateAppendsEditExemptGitignore (sty_f115e6bf AC3): a non-empty
// edit_exempt_paths that predates the managed .gitignore default is appended
// to without clobbering operator additions; empty list is left alone; second
// run is a no-op.
func TestMigrateAppendsEditExemptGitignore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.toml"), []byte("[executor]\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Predating list: operator added .claude/; missing .gitignore.
	tomlBody := `[gate]
edit_exempt_paths = [".satelle/", ".claude/"]
`
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// Home-keyed DB so migrate has something to validate against without relocate.
	homeDB := filepath.Join(home, config.RepoKey(repo), config.DefaultDBName)
	if err := os.MkdirAll(filepath.Dir(homeDB), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(homeDB)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	a := &app.App{
		Config:     config.Config{},
		RepoRoot:   repo,
		DataDir:    dataDir,
		RuntimeDir: filepath.Dir(homeDB),
		DBPath:     homeDB,
	}

	// Dry-run names the config step.
	var dry strings.Builder
	if err := runMigrate(&dry, a, false); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, dry.String())
	}
	if !strings.Contains(dry.String(), "edit_exempt_paths") && !strings.Contains(dry.String(), ".gitignore") {
		t.Errorf("dry-run plan should name config/.gitignore converge:\n%s", dry.String())
	}
	// File unchanged after dry-run.
	raw, _ := os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	if string(raw) != tomlBody {
		t.Fatalf("dry-run rewrote toml:\n%s", raw)
	}

	// Apply: append .gitignore, keep .claude/.
	var apply strings.Builder
	if err := runMigrate(&apply, a, true); err != nil {
		t.Fatalf("apply: %v\n%s", err, apply.String())
	}
	got, err := os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `edit_exempt_paths = [".satelle/", ".claude/", ".gitignore"]`) {
		t.Errorf("want append-only merge, got:\n%s", s)
	}
	if !strings.Contains(apply.String(), "edit_exempt_paths") {
		t.Errorf("apply report should name the update:\n%s", apply.String())
	}

	// Idempotent second run.
	var again strings.Builder
	if err := runMigrate(&again, a, true); err != nil {
		t.Fatalf("second run: %v\n%s", err, again.String())
	}
	if !strings.Contains(again.String(), "already on current structure") {
		t.Errorf("second run should be no-op:\n%s", again.String())
	}
	// Empty list is deliberate opt-out — not converged.
	emptyRepo := t.TempDir()
	emptyData := filepath.Join(emptyRepo, config.DefaultDataDir)
	if err := os.MkdirAll(emptyData, 0o755); err != nil {
		t.Fatal(err)
	}
	emptyToml := "[gate]\nedit_exempt_paths = []\n"
	if err := os.WriteFile(filepath.Join(emptyData, "satelle.toml"), []byte(emptyToml), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ensureEditExemptGitignore(emptyData)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("empty edit_exempt_paths must not be rewritten")
	}
	rawEmpty, _ := os.ReadFile(filepath.Join(emptyData, "satelle.toml"))
	if string(rawEmpty) != emptyToml {
		t.Errorf("empty list clobbered:\n%s", rawEmpty)
	}
}
