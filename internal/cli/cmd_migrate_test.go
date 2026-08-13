package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestMigrateConvergesLegacyFixture (sty_a3915840 AC5): a full legacy layout
// dry-runs a complete plan, --yes converges, and a second run is a no-op.
func TestMigrateConvergesLegacyFixture(t *testing.T) {
	disableServeProbe(t)
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
	if err := runMigrate(&dry, a, false, false); err != nil {
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
	if err := runMigrate(&apply, a, true, false); err != nil {
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
	// Operator hand-made agents.*.bak is NOT residue (sty_0445104b).
	if _, err := os.Stat(filepath.Join(dataDir, "agents.toml.bak")); err != nil {
		t.Fatal("agents.toml.bak must survive migrate — it is the operator's backup, not runtime residue")
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
	if err := runMigrate(&again, a, true, false); err != nil {
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

// TestMigrateAppendsEditExemptManaged (sty_f115e6bf AC3, widened by sty_926cfcdc
// AC5): a non-empty edit_exempt_paths that predates the managed footprint is
// appended to — every missing managed entry, in one rewrite — without clobbering
// operator additions or their order; empty list is left alone; second run is a
// no-op.
func TestMigrateAppendsEditExemptManaged(t *testing.T) {
	disableServeProbe(t)
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
	// Predating list: operator added .claude/ by hand; the rest of the managed
	// footprint (.gitignore, .grok/, .codex/) is missing.
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
	if err := runMigrate(&dry, a, false, false); err != nil {
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

	// Apply: append every missing managed entry, keep .claude/ in its place.
	var apply strings.Builder
	if err := runMigrate(&apply, a, true, false); err != nil {
		t.Fatalf("apply: %v\n%s", err, apply.String())
	}
	got, err := os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `edit_exempt_paths = [".satelle/", ".claude/", ".gitignore", ".grok/", ".codex/", "/tmp/"]`) {
		t.Errorf("want append-only merge, got:\n%s", s)
	}
	if !strings.Contains(apply.String(), "edit_exempt_paths") {
		t.Errorf("apply report should name the update:\n%s", apply.String())
	}

	// Idempotent second run.
	var again strings.Builder
	if err := runMigrate(&again, a, true, false); err != nil {
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
	changed, err := ensureEditExemptManaged(emptyData)
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

// TestMigrateAppendsEditExemptGlobs (sty_fefc88cd AC3): a non-empty
// edit_exempt_globs that predates the managed dump names is appended to
// without clobbering operator additions; an empty list is a deliberate
// opt-out and is left alone.
func TestMigrateAppendsEditExemptGlobs(t *testing.T) {
	disableServeProbe(t)
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
	tomlBody := `[gate]
edit_exempt_paths = ` + defaultEditExemptTOML() + `
edit_exempt_globs = ["notes/*.md"]
`
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
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

	var dry strings.Builder
	if err := runMigrate(&dry, a, false, false); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, dry.String())
	}
	if !strings.Contains(dry.String(), "edit_exempt_globs") {
		t.Errorf("dry-run plan should name glob converge:\n%s", dry.String())
	}
	raw, _ := os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	if string(raw) != tomlBody {
		t.Fatalf("dry-run rewrote toml:\n%s", raw)
	}

	var apply strings.Builder
	if err := runMigrate(&apply, a, true, false); err != nil {
		t.Fatalf("apply: %v\n%s", err, apply.String())
	}
	got, err := os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `edit_exempt_globs = ["notes/*.md", "sty_*_body.md", "sty_*_ac.md"]`) {
		t.Errorf("want append-only glob merge, got:\n%s", s)
	}
	if !strings.Contains(apply.String(), "edit_exempt_globs") {
		t.Errorf("apply report should name the update:\n%s", apply.String())
	}

	var again strings.Builder
	if err := runMigrate(&again, a, true, false); err != nil {
		t.Fatalf("second run: %v\n%s", err, again.String())
	}
	if !strings.Contains(again.String(), "already on current structure") {
		t.Errorf("second run should be no-op:\n%s", again.String())
	}

	emptyRepo := t.TempDir()
	emptyData := filepath.Join(emptyRepo, config.DefaultDataDir)
	if err := os.MkdirAll(emptyData, 0o755); err != nil {
		t.Fatal(err)
	}
	emptyToml := "[gate]\nedit_exempt_globs = []\n"
	if err := os.WriteFile(filepath.Join(emptyData, "satelle.toml"), []byte(emptyToml), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ensureGateListManaged(emptyData, "edit_exempt_globs", managedEditExemptGlobs, managedEditExemptGlobs)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("empty edit_exempt_globs must not be rewritten")
	}
	rawEmpty, _ := os.ReadFile(filepath.Join(emptyData, "satelle.toml"))
	if string(rawEmpty) != emptyToml {
		t.Errorf("empty glob list clobbered:\n%s", rawEmpty)
	}
}

// TestMigrateSeedsAbsentEditExemptGlobs (sty_e8e1879c AC2): a [gate] table
// with edit_exempt_paths but no edit_exempt_globs key receives the managed
// dump names. Empty list stays an opt-out. A non-empty paths list missing
// /tmp/ gains that draft prefix.
func TestMigrateSeedsAbsentEditExemptGlobs(t *testing.T) {
	disableServeProbe(t)
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
	tomlBody := "[gate]\nedit_exempt_paths = [\".satelle/\"]\n"
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
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
		Config: config.Config{}, RepoRoot: repo, DataDir: dataDir,
		RuntimeDir: filepath.Dir(homeDB), DBPath: homeDB,
	}
	var apply strings.Builder
	if err := runMigrate(&apply, a, true, false); err != nil {
		t.Fatalf("apply: %v\n%s", err, apply.String())
	}
	got, err := os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, `edit_exempt_globs = ["sty_*_body.md", "sty_*_ac.md"]`) {
		t.Errorf("absent globs key must materialise dump names:\n%s", s)
	}
	if !strings.Contains(s, `"/tmp/"`) {
		t.Errorf("non-empty paths list must gain /tmp/:\n%s", s)
	}
	if strings.Count(s, "[gate]") != 1 {
		t.Errorf("must not duplicate [gate] table:\n%s", s)
	}
}

func TestMigrateCopiesHostedBindingOntoSync(t *testing.T) {
	disableServeProbe(t)
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
	tomlBody := `[sync]
all = "personal"
[hosted]
server = "https://legacy.example"
project = "legacy-proj"
workspace = "Legacy Team"
`
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	homeDB := filepath.Join(home, config.RepoKey(repo), config.DefaultDBName)
	if err := os.MkdirAll(filepath.Dir(homeDB), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(homeDB)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	a := &app.App{Config: config.Config{}, RepoRoot: repo, DataDir: dataDir, RuntimeDir: filepath.Dir(homeDB), DBPath: homeDB}

	var apply strings.Builder
	if err := runMigrate(&apply, a, true, false); err != nil {
		t.Fatalf("apply: %v\n%s", err, apply.String())
	}
	got, _ := os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	s := string(got)
	for _, want := range []string{`all = "personal"`, `server = "https://legacy.example"`, `project = "legacy-proj"`, `workspace = "Legacy Team"`, "[hosted]"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}

	// Already-set [sync] project is not clobbered.
	clobber := `[sync]
project = "keep-me"
[hosted]
project = "ignore-me"
server = "https://legacy.example"
`
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte(clobber), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ensureHostedBindingOnSync(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected server copy")
	}
	got, _ = os.ReadFile(filepath.Join(dataDir, "satelle.toml"))
	if !strings.Contains(string(got), "[sync]\nproject = \"keep-me\"") {
		t.Errorf("clobbered [sync] project:\n%s", got)
	}

	// Second full migrate is a no-op once [sync] has the leftover keys.
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte(tomlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureHostedBindingOnSync(dataDir); err != nil {
		t.Fatal(err)
	}
	changed, err = ensureHostedBindingOnSync(dataDir)
	if err != nil || changed {
		t.Fatalf("second copy changed=%v err=%v", changed, err)
	}
}

// migrateWorkflowRepo builds a minimal repo whose .satelle/workflows holds the
// given files (name → body), plus the agents/config a migrate plan needs.
func migrateWorkflowRepo(t *testing.T, files map[string]string) (repo, dataDir string) {
	t.Helper()
	disableServeProbe(t)
	t.Setenv("SATELLE_HOME", t.TempDir())
	repo = t.TempDir()
	dataDir = filepath.Join(repo, config.DefaultDataDir)
	wfDir := filepath.Join(dataDir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agents.toml"), []byte("[executor]\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.toml"), []byte("web_port = 8182\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(wfDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo, dataDir
}

const migrateDOTWorkflow = `---
name: legacy-workflow
type: workflow
scope: project
applies_to: ["*"]
description: legacy graph
---
` + "```dot" + `
digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare]
  backlog -> in_progress -> done
}
` + "```" + `
`

const migrateDoneMD = `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "declaration of done"

["*"]
obligations = ["raised", "coded"]
`

const migrateStepMD = `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "step catalogue"

[raised]
status = "backlog"
start = true

[coded]
status = "done"
terminal = true
requires = ["raised"]
`

// TestMigrateRetiresSupersededDOTWorkflows (sty_9835070d AC2): with an authored,
// parseable route source present, migrate lists the superseded graphs in dry-run
// and removes them — and only them — on apply.
func TestMigrateRetiresSupersededDOTWorkflows(t *testing.T) {
	repo, dataDir := migrateWorkflowRepo(t, map[string]string{
		"legacy-workflow.md": migrateDOTWorkflow,
		"done.md":            migrateDoneMD,
		"step.md":            migrateStepMD,
	})
	cfg := config.Config{}
	plan := planMigrate(cfg, repo, dataDir)
	if len(plan.WorkflowRetire) != 1 || plan.WorkflowRetire[0] != "legacy-workflow.md" {
		t.Fatalf("WorkflowRetire = %v, want [legacy-workflow.md]", plan.WorkflowRetire)
	}
	if len(plan.WorkflowConvertPending) != 0 {
		t.Errorf("WorkflowConvertPending = %v, want none", plan.WorkflowConvertPending)
	}
	if plan.empty() {
		t.Error("a retire-only plan must not report empty — it would print \"already on current structure\" and apply nothing")
	}

	a := &app.App{Config: cfg, RepoRoot: repo, DataDir: dataDir}
	var dry strings.Builder
	if err := runMigrate(&dry, a, false, false); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if !strings.Contains(dry.String(), "retire 1 superseded DOT workflow(s)") {
		t.Errorf("dry-run did not list the retire step:\n%s", dry.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "workflows", "legacy-workflow.md")); err != nil {
		t.Error("dry-run must remove nothing")
	}

	var applied strings.Builder
	if err := runMigrate(&applied, a, true, false); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "workflows", "legacy-workflow.md")); !os.IsNotExist(err) {
		t.Error("apply must remove the superseded DOT workflow")
	}
	for _, keep := range []string{"done.md", "step.md"} {
		if _, err := os.Stat(filepath.Join(dataDir, "workflows", keep)); err != nil {
			t.Errorf("apply must not touch %s: %v", keep, err)
		}
	}
	// Idempotent: a converged repo says so, with no outstanding notice.
	var again strings.Builder
	if err := runMigrate(&again, a, false, false); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if !strings.Contains(again.String(), "already on current structure") {
		t.Errorf("converged repo should report already-current:\n%s", again.String())
	}
	if strings.Contains(again.String(), "OUTSTANDING") {
		t.Errorf("converged repo must not report an outstanding conversion:\n%s", again.String())
	}
}

// TestMigrateReportsOutstandingConversion: with DOT workflows and no route
// source, migrate must say the conversion is OUTSTANDING and remove nothing —
// in dry-run AND on apply. It never derives a route: that is interpretation, and
// it is authored and reviewed, not generated.
func TestMigrateReportsOutstandingConversion(t *testing.T) {
	repo, dataDir := migrateWorkflowRepo(t, map[string]string{
		"legacy-workflow.md": migrateDOTWorkflow,
	})
	cfg := config.Config{}
	plan := planMigrate(cfg, repo, dataDir)
	if len(plan.WorkflowRetire) != 0 {
		t.Errorf("WorkflowRetire = %v, want none without a route source", plan.WorkflowRetire)
	}
	if len(plan.WorkflowConvertPending) != 1 {
		t.Fatalf("WorkflowConvertPending = %v, want [legacy-workflow.md]", plan.WorkflowConvertPending)
	}

	a := &app.App{Config: cfg, RepoRoot: repo, DataDir: dataDir}
	for _, yes := range []bool{false, true} {
		var out strings.Builder
		// The apply path also re-validates, and a leftover graph now FAILS that
		// check — a workflows doc that is not a route source governs nothing
		// (sty_d953c5d8). The outstanding-conversion report is what this test is
		// about, and it must be printed either way.
		err := runMigrate(&out, a, yes, false)
		if err != nil && !strings.Contains(err.Error(), "failed validation") {
			t.Fatalf("yes=%v: %v", yes, err)
		}
		if !strings.Contains(out.String(), "workflow conversion OUTSTANDING") {
			t.Errorf("yes=%v: outstanding conversion not surfaced:\n%s", yes, out.String())
		}
		if _, err := os.Stat(filepath.Join(dataDir, "workflows", "legacy-workflow.md")); err != nil {
			t.Errorf("yes=%v: nothing may be removed while the conversion is outstanding", yes)
		}
	}
}

// TestMigrateRefusesRetireOnMalformedRouteSource: a half-authored route source
// must not be read as a working one — deleting the only remaining lifecycle
// because a broken done.md happened to be on disk is the failure this step must
// not have.
func TestMigrateRefusesRetireOnMalformedRouteSource(t *testing.T) {
	repo, dataDir := migrateWorkflowRepo(t, map[string]string{
		"legacy-workflow.md": migrateDOTWorkflow,
		"done.md":            migrateDoneMD,
		"step.md":            "---\nname: step\ntype: workflow\n---\n# Step catalogue\n\n## backlog\nnot-a-key\n",
	})
	plan := planMigrate(config.Config{}, repo, dataDir)
	if len(plan.WorkflowRetire) != 0 {
		t.Errorf("WorkflowRetire = %v — a malformed route source must retire nothing", plan.WorkflowRetire)
	}
	if len(plan.WorkflowConvertPending) != 1 {
		t.Errorf("WorkflowConvertPending = %v, want the DOT still pending", plan.WorkflowConvertPending)
	}
}

// TestMigrateRewritesFilePairWorkflowStamps (sty_81bb0dde): the `workflow:` tag
// names a lifecycle, not the two files it is derived from. Stories stamped with
// the retired file-pair spelling are rewritten to the derived route's name, every
// other tag is left alone, and a second run is a no-op.
func TestMigrateRewritesFilePairWorkflowStamps(t *testing.T) {
	disableServeProbe(t)
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	repo := t.TempDir()
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(filepath.Join(dataDir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "workflows", "agents.toml"),
		[]byte("[executor]\nrole = \"agent\"\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	homeDB := filepath.Join(home, config.RepoKey(repo), config.DefaultDBName)
	if err := os.MkdirAll(filepath.Dir(homeDB), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(homeDB)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	// Three shapes: the original markdown pair, the brief TOML pair, and a story
	// stamped with a real workflow name that must NOT be touched.
	stale, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "stale md pair",
		Tags: []string{"area:substrate", "workflow:done.md+step.md", "surface:cli"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	staleTOML, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "stale toml pair",
		Tags: []string{"workflow:done.toml+step.toml"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	named, err := db.Stories.Create(ctx, workitem.CreateInput{
		Kind: workitem.KindStory, Title: "authored workflow",
		Tags: []string{"workflow:gov-workflow"},
	}, now)
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

	// Dry-run names both stale stories and writes nothing.
	var dry strings.Builder
	if err := runMigrate(&dry, a, false, false); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, dry.String())
	}
	for _, id := range []string{stale.ID, staleTOML.ID} {
		if !strings.Contains(dry.String(), id) {
			t.Errorf("dry-run plan should name %s:\n%s", id, dry.String())
		}
	}
	if strings.Contains(dry.String(), named.ID) {
		t.Errorf("dry-run must not touch a story stamped with a workflow name:\n%s", dry.String())
	}
	reopened, err := store.Open(homeDB)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Stories.Get(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = reopened.Close()
	if wf := wfgovern.StampedWorkflowName(got); wf != "done.md+step.md" {
		t.Fatalf("dry-run rewrote the stamp: %q", wf)
	}

	// Apply.
	var apply strings.Builder
	if err := runMigrate(&apply, a, true, false); err != nil {
		t.Fatalf("apply: %v\n%s", err, apply.String())
	}
	after, err := store.Open(homeDB)
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	md, err := after.Stories.Get(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wf := wfgovern.StampedWorkflowName(md); wf != wfgovern.DerivedRouteName {
		t.Errorf("stamp = %q, want %q", wf, wfgovern.DerivedRouteName)
	}
	// Every other tag survives, in order.
	wantTags := []string{"area:substrate", "workflow:" + wfgovern.DerivedRouteName, "surface:cli"}
	if strings.Join(md.Tags, ",") != strings.Join(wantTags, ",") {
		t.Errorf("tags = %v, want %v", md.Tags, wantTags)
	}
	tm, err := after.Stories.Get(ctx, staleTOML.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wf := wfgovern.StampedWorkflowName(tm); wf != wfgovern.DerivedRouteName {
		t.Errorf("toml-pair stamp = %q, want %q", wf, wfgovern.DerivedRouteName)
	}
	keep, err := after.Stories.Get(ctx, named.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wf := wfgovern.StampedWorkflowName(keep); wf != "gov-workflow" {
		t.Errorf("named workflow stamp was rewritten to %q", wf)
	}

	// Idempotent.
	var again strings.Builder
	if err := runMigrate(&again, a, true, false); err != nil {
		t.Fatalf("second run: %v\n%s", err, again.String())
	}
	if !strings.Contains(again.String(), "already on current structure") {
		t.Errorf("second run should be a no-op:\n%s", again.String())
	}
}

// TestListLegacyResidueLeavesAgentsBak proves sty_0445104b: operator
// agents.*.bak under the repo is not classified as legacy residue.
func TestListLegacyResidueLeavesAgentsBak(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "agents.toml.bak"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(dataDir, "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, "agents.toml.codex-broken.bak"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Real residue still listed.
	if err := os.WriteFile(filepath.Join(dataDir, "satelle.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := listLegacyResidue(dataDir)
	for _, p := range got {
		if strings.Contains(p, ".bak") {
			t.Fatalf("listLegacyResidue must not include operator backups, got %v", got)
		}
	}
	foundDB := false
	for _, p := range got {
		if p == "satelle.db" {
			foundDB = true
		}
	}
	if !foundDB {
		t.Fatalf("listLegacyResidue should still list satelle.db, got %v", got)
	}
}

func TestMigrateRetiredRefsRenameAndRemoval(t *testing.T) {
	// Full runMigrate path (AC1 migrate channel + AC2 dry-run never writes).
	repo, dataDir := migrateWorkflowRepo(t, map[string]string{
		"done.md": migrateDoneMD,
		"step.md": migrateStepMD,
	})
	_ = os.MkdirAll(filepath.Join(dataDir, "workflows"), 0o755)
	_ = os.WriteFile(filepath.Join(dataDir, "workflows", "agents.toml"), []byte("[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n[reviewer]\nrole = \"reviewer\"\ncommand = \"echo\"\n"), 0o644)
	prin := filepath.Join(dataDir, "principles")
	if err := os.MkdirAll(prin, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: cite\nscope: project\ntype: principle\n---\n\nSee [[satelle-dot-standard]] and [[satelle-configuration-over-code]].\n"
	path := filepath.Join(prin, "cite.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{}
	plan := planMigrate(cfg, repo, dataDir)
	if len(plan.RetiredRefs) < 2 {
		t.Fatalf("RetiredRefs = %+v, want >=2", plan.RetiredRefs)
	}
	if plan.empty() {
		t.Fatal("plan with retired refs must not be empty — would print already-current")
	}

	a := &app.App{Config: cfg, RepoRoot: repo, DataDir: dataDir}
	var dry strings.Builder
	if err := runMigrate(&dry, a, false, false); err != nil {
		t.Fatalf("dry-run: %v\n%s", err, dry.String())
	}
	ds := dry.String()
	if !strings.Contains(ds, "retired refs") || !strings.Contains(ds, "satelle-dot-standard") ||
		!strings.Contains(ds, "satelle-configuration-over-code") {
		t.Fatalf("dry plan missing retired refs:\n%s", ds)
	}
	if !strings.Contains(ds, "--yes rewrites") || !strings.Contains(ds, "edit by hand") {
		t.Fatalf("dry plan must distinguish rename vs removal remedies:\n%s", ds)
	}
	afterDry, _ := os.ReadFile(path)
	if string(afterDry) != body {
		t.Fatal("dry-run must leave authored substrate byte-identical")
	}

	var apply strings.Builder
	// apply may fail later validation; retired rewrites happen before validate.
	_ = runMigrate(&apply, a, true, false)
	as := apply.String()
	after, _ := os.ReadFile(path)
	s := string(after)
	if strings.Contains(s, "satelle-dot-standard") {
		t.Fatalf("rename not applied under --yes: %s\nreport:\n%s", s, as)
	}
	if !strings.Contains(s, "[[satelle-route-standard]]") {
		t.Fatalf("replacement missing: %s", s)
	}
	if !strings.Contains(s, "satelle-configuration-over-code") {
		t.Fatalf("no-replacement was rewritten: %s", s)
	}
	if !strings.Contains(as, "rewrote") || !strings.Contains(as, "leave") {
		t.Fatalf("apply report:\n%s", as)
	}
}
