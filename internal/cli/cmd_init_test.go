package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/structure"
)

// isolateUserHome pins HOME and SATELLE_HOME to a disposable dir for this test
// so init's Grok-compat write (~/.grok/config.toml) and the home-keyed runtime
// plane cannot touch the developer's real config/home (sty_24b32127,
// sty_c36c211f). Idempotent within a test.
func isolateUserHome(t *testing.T) {
	t.Helper()
	if os.Getenv("SATELLE_INIT_TEST_HOME") != "" {
		// Still enforce SATELLE_HOME — GlobalDir panics under test without it.
		if strings.TrimSpace(os.Getenv("SATELLE_HOME")) == "" {
			t.Setenv("SATELLE_HOME", t.TempDir())
		}
		return
	}
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("SATELLE_HOME", h)
	t.Setenv("SATELLE_INIT_TEST_HOME", h)
}

// runInitTest is runInit with HOME/SATELLE_HOME isolated (see isolateUserHome).
// Registers the repo in the local workspace registry by default (sty_3bdbdc38).
func runInitTest(t *testing.T, out io.Writer, repo string) error {
	t.Helper()
	isolateUserHome(t)
	return runInit(out, repo, false)
}

func TestRunInitScaffolds(t *testing.T) {
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Config + empty authored dirs under the repo; DB on the home-keyed runtime
	// plane (sty_4660bbe1). Virtual sparse defaults (sty_29e5a9a5): no unedited
	// default markdown is seeded.
	for _, rel := range []string{
		".satelle/satelle.toml",
		".satelle/agents.toml",
		".satelle/documents/README.md",
		".satelle/workflows/README.md",
		".satelle/principles/README.md",
		".satelle/skills/README.md",
		".satelle/tasks/README.md",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// No unedited default seeds.
	for _, rel := range []string{
		".satelle/skills/satelle-step-summary.md",
		".satelle/workflows/satelle-baseline-workflow.md",
		".satelle/principles/satelle-agent-goals.md",
		".satelle/tasks/tsk_example1.md",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err == nil {
			t.Errorf("init must not seed unedited default %s (sty_29e5a9a5)", rel)
		}
	}
	// Tasks plane carve-out: default task headers are still seeded (gates need files).
	if _, err := os.Stat(filepath.Join(repo, ".satelle/tasks/tsk_substrate-audit.md")); err != nil {
		t.Errorf("init must still seed tasks/tsk_substrate-audit.md: %v", err)
	}
	// Runtime DB is under ~/.satelle/<repo-key>/ (HOME isolated by runInitTest).
	homeDB := filepath.Join(config.GlobalDir(), config.RepoKey(repo), config.DefaultDBName)
	if _, err := os.Stat(homeDB); err != nil {
		t.Errorf("missing home-keyed db %s: %v", homeDB, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", config.DefaultDBName)); err == nil {
		t.Error("init must not write satelle.db under the repo")
	}
	// The removed .satelle/stories mirror must NOT be recreated (sty_746a0c98).
	if _, err := os.Stat(filepath.Join(repo, ".satelle/stories")); err == nil {
		t.Error("init must not scaffold .satelle/stories — the markdown mirror was removed")
	}

	// gitignore keeps local.toml + pinned binary; runtime paths left the repo (AC4).
	gi, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if strings.Contains(string(gi), ".satelle/satelle.db") {
		t.Error("gitignore must not list satelle.db — runtime is home-keyed")
	}
	if strings.Contains(string(gi), ".satelle/logs/") {
		t.Error("gitignore must not list .satelle/logs/ — runtime is home-keyed")
	}
	if !strings.Contains(string(gi), ".satelle/satelle.local.toml") {
		t.Error("gitignore missing satelle.local.toml entry")
	}
	if strings.Contains(string(gi), "\n.satelle/satelle.toml\n") {
		t.Error("gitignore should not ignore the committed toml")
	}

	// Report shows the home-keyed db creation.
	if !strings.Contains(out.String(), homeDB) {
		t.Errorf("report missing home-keyed db path %s:\n%s", homeDB, out.String())
	}

	// init ends by PROVING the deployment green (sty_d0d6bb67): the validation
	// pass runs and the fresh seeded system validates.
	for _, want := range []string{"Validating the deployed system:", "PASS  deployed system validates green"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// TestRunInitSeedsActiveEditExemptPaths proves the scaffold satelle.toml ships an
// ACTIVE (uncommented) [gate] edit_exempt_paths seeded with .satelle/ and
// .gitignore (sty_8c3d345c / sty_f115e6bf). Exemption is config, not code — a
// fresh repo keeps authored substrate and satelle-managed .gitignore editable
// via this seeded config rather than a hardcoded case in the binary.
func TestRunInitSeedsActiveEditExemptPaths(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(repo, ".satelle", "satelle.toml"))
	if err != nil {
		t.Fatalf("read scaffold satelle.toml: %v", err)
	}
	tomlSrc := string(body)
	// The [gate] table and edit_exempt_paths must be ACTIVE (no leading '#') and
	// seed .satelle/ + .gitignore.
	for _, want := range []string{"\n[gate]\n", "edit_exempt_paths = [\".satelle/\", \".gitignore\"]", "allow_outside_tree_edits"} {
		if !strings.Contains(tomlSrc, want) {
			t.Errorf("scaffold satelle.toml missing active %q:\n%s", want, tomlSrc)
		}
	}
	// Parse it to confirm the seeded values actually resolve as exempt prefixes.
	var cfg config.Config
	if _, err := toml.Decode(tomlSrc, &cfg); err != nil {
		t.Fatalf("scaffold satelle.toml does not parse: %v", err)
	}
	got := cfg.ResolveEditExemptPaths(repo)
	if len(got) != 2 {
		t.Fatalf("ResolveEditExemptPaths = %v, want 2 entries", got)
	}
	if !strings.HasSuffix(got[0], ".satelle") {
		t.Errorf("ResolveEditExemptPaths[0] = %q, want ending .satelle", got[0])
	}
	if !strings.HasSuffix(got[1], ".gitignore") {
		t.Errorf("ResolveEditExemptPaths[1] = %q, want ending .gitignore", got[1])
	}
}

// TestInstallAliasRemoved: satelle install is no longer an alias of init
// (breaking surface). The retiredNames guard names satelle init.
func TestInstallAliasRemoved(t *testing.T) {
	root := NewRootCmd()
	initCmd, _, err := root.Find([]string{"init"})
	if err != nil {
		t.Fatalf("find init: %v", err)
	}
	for _, a := range initCmd.Aliases {
		if a == "install" {
			t.Fatalf("install alias must be removed, still on init: %v", initCmd.Aliases)
		}
	}
	if _, _, err := root.Find([]string{"install"}); err == nil {
		t.Fatal("install must not resolve as a command")
	}
	msg := retiredNameMessage([]string{"install"})
	if !strings.Contains(msg, "satelle init") {
		t.Fatalf("retired message: %q", msg)
	}
}

// TestRunInitAdvisorySkillsAreVirtual: advisory skills resolve from the binary
// without seeding (sty_29e5a9a5), even beside an authored workflow set.
func TestRunInitAdvisorySkillsAreVirtual(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	own := "---\nname: my-workflow\ntype: workflow\ndescription: my own lifecycle\napplies_to: [\"*\"]\nscope: project\n---\n\n```dot\ndigraph w {\n  backlog -> in_progress -> done\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "my-workflow.md"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	p := filepath.Join(repo, ".satelle", "skills", "satelle-workflow-advisor.md")
	if _, err := os.Stat(p); err == nil {
		t.Fatal("advisor skill must not be seeded onto disk")
	}
	body, ok := embeddedDefault("skills", "satelle-workflow-advisor")
	if !ok {
		t.Fatal("advisor skill missing from EmbeddedDefaults")
	}
	if problems := structure.Doc("skills", "satelle-workflow-advisor", body, nil); len(problems) > 0 {
		t.Errorf("embedded advisor skill fails its structure check: %v", problems)
	}
}

// TestRunInitFailsValidationOnBrokenSubstrate: init on a repo whose authored
// substrate does not validate exits non-zero, naming the failures — the runtime
// refuses broken configuration, so init must not report success over it
// (sty_d0d6bb67).
func TestRunInitFailsValidationOnBrokenSubstrate(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A structurally broken authored workflow (missing type/description/scope/DOT).
	if err := os.WriteFile(filepath.Join(wfDir, "broken.md"), []byte("---\nname: broken\n---\n# broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err := runInitTest(t, &out, repo)
	if err == nil {
		t.Fatal("init must exit non-zero when the deployed system fails validation")
	}
	if !strings.Contains(err.Error(), "failed validation") {
		t.Errorf("error should say the deployed system failed validation: %v", err)
	}
	if !strings.Contains(out.String(), "FAIL  workflows/broken") {
		t.Errorf("report should name the failing artifact:\n%s", out.String())
	}
}

func TestRunInitIdempotent(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	// Capture a user edit to the toml; a second init must not clobber it.
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	marker := "\nweb_port = 9123\n"
	orig, _ := os.ReadFile(tomlPath)
	if err := os.WriteFile(tomlPath, append(orig, []byte(marker)...), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("second runInit: %v", err)
	}
	// Everything reported as already present.
	if strings.Contains(out.String(), "  + ") {
		t.Errorf("second init created something:\n%s", out.String())
	}
	// The user edit survived.
	after, _ := os.ReadFile(tomlPath)
	if !strings.Contains(string(after), "web_port = 9123") {
		t.Error("second init clobbered the user's toml edit")
	}

	// An authored task the operator wrote survives re-init (never clobbered),
	// even though init no longer seeds any example task (sty_04ec1fe6).
	taskPath := filepath.Join(repo, ".satelle", "tasks", "tsk_mine.md")
	authored := "---\nid: tsk_mine\ntype: task\nstatus: in_progress\n---\n\n# Mine\n\nACTION; VERIFICATION.\n"
	if err := os.WriteFile(taskPath, []byte(authored), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(taskPath); string(got) != authored {
		t.Errorf("re-init clobbered the authored task:\n%s", got)
	}
}

// TestRunInitSeedsAuditTask asserts the embedded substrate-audit task is still
// seeded (tasks plane carve-out — coded gates require an on-disk header) and is
// re-runnable from done (sty_d4360e90). Workflows/skills stay virtual.
func TestRunInitSeedsAuditTask(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	dataDir := filepath.Join(repo, ".satelle")
	body, err := os.ReadFile(filepath.Join(dataDir, "tasks", "tsk_substrate-audit.md"))
	if err != nil {
		t.Fatalf("init did not seed tasks/tsk_substrate-audit.md: %v", err)
	}
	if problems := structure.CheckTask(string(body)); len(problems) > 0 {
		t.Errorf("seeded audit task fails CheckTask: %v", problems)
	}
	if !bytes.Contains(body, []byte("\nstatus: done\n")) {
		t.Errorf("seeded audit task must sit at status: done, got:\n%s", body)
	}
	// Workflows/skills still virtual.
	if fileExists(filepath.Join(dataDir, "workflows", "satelle-baseline-workflow.md")) {
		t.Error("workflows must not be seeded")
	}

	var docs []docindex.Doc
	for _, wf := range defaultSolutionWorkflows {
		b, ok := embeddedDefault("workflows", wf)
		if !ok {
			t.Fatalf("embedded workflow %s missing", wf)
		}
		docs = append(docs, docindex.Doc{Name: wf, Body: b})
	}
	ordered := agentstep.OrderedWorkflows(docs, "task")
	if len(ordered) == 0 || ordered[0].Name != "satelle-task-workflow" {
		t.Errorf("audit task does not resolve to satelle-task-workflow: %+v", ordered)
	}

	skillBody, ok := embeddedDefault("skills", "satelle-task-validate-before-review")
	if !ok {
		t.Fatal("embedded task-validate-before skill missing")
	}
	payload := `{"story":{"id":"exe_test0001","kind":"execution","title":"audit run","status":"backlog","parent_id":"tsk_substrate-audit","tags":[],"created_at":"2026-07-08T00:00:00Z","updated_at":"2026-07-08T00:00:00Z"},"from":"backlog","to":"in_progress","review_skill":"satelle-task-validate-before-review"}`
	cmd := exec.Command("sh", "-c", checkScript(t, skillBody))
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(payload)
	var gate bytes.Buffer
	cmd.Stdout = &gate
	cmd.Stderr = &gate
	if err := cmd.Run(); err != nil {
		t.Errorf("begin-run gate rejected a new run under the done audit task: %v\n%s", err, gate.String())
	}
}

// checkScript extracts the self-contained ```check block a functional-check
// skill carries — the command an Engine runs as the gate. Test-local mirror of
// agentstep.bodyCheckBlock (unexported) so this test exercises the REAL shipped
// gate script instead of re-deriving its logic.
func checkScript(t *testing.T, skillBody string) string {
	t.Helper()
	lines := strings.Split(skillBody, "\n")
	in := false
	var out []string
	for _, ln := range lines {
		trim := strings.TrimSpace(ln)
		if !in {
			if strings.HasPrefix(trim, "```") {
				if info := strings.TrimPrefix(trim, "```"); info == "check" || strings.HasPrefix(info, "check ") {
					in = true
				}
			}
			continue
		}
		if strings.HasPrefix(trim, "```") {
			break
		}
		out = append(out, ln)
	}
	s := strings.TrimSpace(strings.Join(out, "\n"))
	if s == "" {
		t.Fatal("task-validate-before skill carries no ```check block")
	}
	return s
}

// defaultSolutionSkills is every gate/executor skill the seeded default solution
// references — the set a fresh repo must hold on disk so nothing dangles. (The
// story reviewers seed via the baseline lifecycle's declared gates — its intent,
// done, and cancel reviews plus the coded estimate check and the step summary —
// and the parent workflow's own close gate.)
var defaultSolutionSkills = []string{
	"satelle-estimate-actual-review",
	"satelle-step-summary",
	"satelle-story-blocked-review",
	"satelle-story-cancel-review",
	"satelle-story-done-review",
	"satelle-story-intent-review",
	"satelle-task-validate-before-review",
	"satelle-task-validate-after-review",
}

// TestRunInitVirtualDefaultSolution asserts a fresh init does NOT seed the
// default solution onto disk (sty_29e5a9a5) yet validates green — defaults resolve
// virtually and skill refs resolve against the embedded set.
func TestRunInitVirtualDefaultSolution(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	dataDir := filepath.Join(repo, ".satelle")

	for _, wf := range defaultSolutionWorkflows {
		if fileExists(filepath.Join(dataDir, "workflows", wf+".md")) {
			t.Errorf("init must not seed workflows/%s.md (virtual defaults)", wf)
		}
	}
	for _, sk := range defaultSolutionSkills {
		if fileExists(filepath.Join(dataDir, "skills", sk+".md")) {
			t.Errorf("init must not seed skills/%s.md (virtual defaults)", sk)
		}
	}

	// Embedded defaults are structure-conformant and skills resolve virtually.
	resolve := skillResolves(dataDir)
	var docs []docindex.Doc
	for _, wf := range defaultSolutionWorkflows {
		body, ok := embeddedDefault("workflows", wf)
		if !ok {
			t.Fatalf("embedded workflow %s missing", wf)
		}
		for _, p := range structure.Doc("workflows", wf, body, resolve) {
			t.Errorf("embedded workflows/%s: %s", wf, p)
		}
		docs = append(docs, docindex.Doc{Name: wf, Body: body})
	}
	for _, p := range agentstep.WorkflowConsistency(docs, resolve) {
		t.Errorf("embedded workflow set inconsistent: %s", p)
	}
	for _, sk := range defaultSolutionSkills {
		body, ok := embeddedDefault("skills", sk)
		if !ok {
			t.Fatalf("embedded skill %s missing", sk)
		}
		for _, p := range structure.Doc("skills", sk, body, nil) {
			t.Errorf("embedded skills/%s: %s", sk, p)
		}
	}

	// An execution resolves to the task-execution workflow out of the box.
	ordered := agentstep.OrderedWorkflows(docs, "execution")
	if len(ordered) == 0 || ordered[0].Name != "satelle-task-workflow" {
		t.Errorf("execution does not resolve to satelle-task-workflow: %+v", ordered)
	}

	baseBody, _ := embeddedDefault("workflows", "satelle-baseline-workflow")
	for _, state := range []string{"commit", "push", "committed", "integration"} {
		if strings.Contains(baseBody, state+" [") || strings.Contains(baseBody, state+"  [") {
			t.Errorf("generic base workflow declares extra state %q", state)
		}
	}
	for _, gate := range []string{"satelle-story-intent-review", "satelle-story-done-review", "satelle-story-cancel-review", "satelle-estimate-actual-review"} {
		if !strings.Contains(baseBody, gate) {
			t.Errorf("generic base workflow must reference gate %q", gate)
		}
	}
	if strings.Contains(baseBody, "satelle-code-ac-review") {
		t.Error("generic base workflow must not reference satelle-code-ac-review")
	}
	estBody, _ := embeddedDefault("skills", "satelle-estimate-actual-review")
	if !strings.Contains(estBody, "```check") {
		t.Error("estimate skill must carry a self-contained ```check block")
	}
}

// TestRunInitBesideAuthoredWorkflowNoSeeds (sty_29e5a9a5): with virtual defaults,
// init does not seed sibling workflow/skill files beside an authored workflow.
// Gate skills resolve virtually; validation is green; the authored file is untouched.
func TestRunInitBesideAuthoredWorkflowNoSeeds(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(wfDir, "my-workflow.md")
	ownBody := "---\nname: my-workflow\ntype: workflow\ndescription: my own lifecycle\napplies_to: [\"*\"]\nscope: project\n---\n\n# mine\n\n```dot\ndigraph w {\n  backlog -> in_progress -> done\n  estimate [agent=reviewer, prompt=\"@skill:satelle-estimate-actual-review\", on=\"in_progress,done\"]\n}\n```\n"
	if err := os.WriteFile(own, []byte(ownBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	// No default seeds.
	for _, rel := range []string{
		"workflows/satelle-baseline-workflow.md",
		"workflows/satelle-parent-workflow.md",
		"skills/satelle-estimate-actual-review.md",
	} {
		if fileExists(filepath.Join(repo, ".satelle", rel)) {
			t.Errorf("init seeded %s — virtual defaults must not write unedited copies", rel)
		}
	}
	if got, _ := os.ReadFile(own); !strings.Contains(string(got), "# mine") {
		t.Error("authored workflow was modified")
	}
}

// TestRunInitVirtualGateSkillResolves (sty_29e5a9a5): a workflow that references
// an embedded gate skill validates green without the skill file on disk.
func TestRunInitVirtualGateSkillResolves(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	own := "---\nname: my-workflow\ntype: workflow\ndescription: my own lifecycle\napplies_to: [\"*\"]\nscope: project\n---\n\n# mine\n\n```dot\ndigraph w {\n  backlog -> in_progress -> done\n  estimate [agent=reviewer, prompt=\"@skill:satelle-estimate-actual-review\", on=\"in_progress,done\"]\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "my-workflow.md"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("init must validate green with virtual gate skill: %v", err)
	}
	if fileExists(filepath.Join(repo, ".satelle", "skills", "satelle-estimate-actual-review.md")) {
		t.Error("init must not seed the virtual gate skill onto disk")
	}
}

func TestEnsureGitignoreAppendsOnce(t *testing.T) {
	repo := t.TempDir()
	giPath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(giPath, []byte("node_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := ensureGitignore(repo)
	if err != nil || !added {
		t.Fatalf("first ensureGitignore: added=%v err=%v", added, err)
	}
	added2, _ := ensureGitignore(repo)
	if added2 {
		t.Error("second ensureGitignore should be a no-op")
	}
	gi, _ := os.ReadFile(giPath)
	if !strings.Contains(string(gi), "node_modules/") {
		t.Error("existing .gitignore content lost")
	}
	if strings.Count(string(gi), gitignoreMarker) != 1 {
		t.Error("managed block appended more than once")
	}
	// Fresh append must use the home-keyed form (no runtime db ignore entries).
	if strings.Contains(string(gi), ".satelle/satelle.db") {
		t.Error("gitignore must not ignore .satelle/satelle.db (home-keyed)")
	}
}

// TestRunInitConvergesStaleGitignore (sty_87c8a69c AC1/AC4): re-running init on a
// repo with a pre-relocation managed block rewrites between the markers and
// leaves operator content outside them.
func TestRunInitConvergesStaleGitignore(t *testing.T) {
	repo := t.TempDir()
	// Pre-seed a minimal satelle tree so init is a re-run, not a first scaffold
	// that also creates agents/db. Stale managed block is the subject under test.
	if err := os.MkdirAll(filepath.Join(repo, ".satelle"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "satelle.toml"), []byte("web_port = 8181\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"), []byte("[executor]\nharness = \"in-loop\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := "node_modules/\n# >>> satelle (managed) >>>\n.satelle/satelle.db\n.satelle/logs/\n.satelle/backups/\n.satelle/stories/\n# <<< satelle (managed) <<<\n# keep-me\n"
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	gi, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(gi)
	if !strings.Contains(s, "node_modules/") {
		t.Error("content before markers must survive")
	}
	if !strings.Contains(s, "# keep-me") {
		t.Error("content after markers must survive")
	}
	for _, staleEntry := range []string{".satelle/satelle.db", ".satelle/logs/", ".satelle/backups/", ".satelle/stories/"} {
		if strings.Contains(s, staleEntry) {
			t.Errorf("stale ignore %q must be gone after init re-run:\n%s", staleEntry, s)
		}
	}
	if !strings.Contains(s, ".satelle/satelle.local.toml") {
		t.Errorf("current block must list local.toml:\n%s", s)
	}
	if strings.Count(s, gitignoreMarker) != 1 {
		t.Error("exactly one managed block expected")
	}
}

// TestInitLongHelpNamesHomeKeyedRuntime (sty_87c8a69c AC3).
func TestInitLongHelpNamesHomeKeyedRuntime(t *testing.T) {
	// The cobra Long is in package init; assert via the command surface.
	root := NewRootCmd()
	var initCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "init" {
			initCmd = c
			break
		}
	}
	if initCmd == nil {
		t.Fatal("init command missing")
	}
	long := initCmd.Long
	if !strings.Contains(long, "~/.satelle/<repo-key>") && !strings.Contains(long, "home-keyed") {
		t.Errorf("init help must name home-keyed runtime, got:\n%s", long)
	}
	// Must not claim the DB lives at .satelle/satelle.db as the default layout.
	if strings.Contains(long, "database at .satelle/satelle.db") {
		t.Errorf("init help still claims in-repo satelle.db:\n%s", long)
	}
}

func TestEnsureClaudeHooksIdempotent(t *testing.T) {
	repo := t.TempDir()
	created, _, _, err := ensureClaudeHooks(repo)
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v, want created", created, err)
	}
	path := filepath.Join(repo, ".claude", "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	for _, want := range []string{
		".satelle/hooks/pretooluse-gate-claude.sh",
		".satelle/hooks/pretooluse-commitgate-claude.sh",
		"PATH=$HOME/.local/bin:$PATH satelle hook prompt",
		"PATH=$HOME/.local/bin:$PATH satelle hook stopcheck",
		"UserPromptSubmit",
		"Stop",
		"satelle hook context",
		"Edit|Write",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("settings.json missing %q", want)
		}
	}
	if strings.Contains(string(b), "|| exit 2") {
		t.Errorf("settings must not use bare '|| exit 2'")
	}
	if strings.Contains(string(b), "sh -c ") {
		t.Errorf("settings must not use inline sh -c PreToolUse")
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "hooks", "pretooluse-gate-claude.sh")); err != nil {
		t.Errorf("gate script not written: %v", err)
	}
	// An existing file that LACKS the reinforcement hooks is HEALED, not left
	// ungated (sty_949e8739): prompt/stopcheck are appended and other keys are
	// preserved — otherwise a repo initialized before the reinforcement hooks
	// shipped would never gain them, re-opening the bypass.
	if err := os.WriteFile(path, []byte("{\"custom\":true}"), 0o644); err != nil {
		t.Fatal(err)
	}
	created2, updated2, _, err := ensureClaudeHooks(repo)
	if err != nil || created2 {
		t.Fatalf("second call: created=%v err=%v, want not created", created2, err)
	}
	if len(updated2) == 0 {
		t.Errorf("heal: expected reinforcement hooks to be reported as added")
	}
	b2, _ := os.ReadFile(path)
	for _, want := range []string{`"custom": true`, "satelle hook prompt", "satelle hook stopcheck"} {
		if !strings.Contains(string(b2), want) {
			t.Errorf("heal: settings.json missing %q after heal:\n%s", want, b2)
		}
	}
	// Idempotent: a THIRD call adds nothing and rewrites nothing (already healed).
	before, _ := os.ReadFile(path)
	created3, updated3, _, err := ensureClaudeHooks(repo)
	after, _ := os.ReadFile(path)
	if err != nil || created3 || len(updated3) != 0 || string(before) != string(after) {
		t.Fatalf("third call not idempotent: created=%v updated=%v err=%v changed=%v",
			created3, updated3, err, string(before) != string(after))
	}
}

// TestRunInitAgentGuidance asserts init ends its report with the agent-facing
// note when the repo carries an agent instruction file (sty_4c406061): the
// reading agent is told to add/update a satelle section preferring `satelle
// help` — and the file itself is never modified.
func TestRunInitAgentGuidance(t *testing.T) {
	cases := []struct {
		name  string
		files []string
	}{
		{"claude.md present", []string{"CLAUDE.md"}},
		{"agents.md present", []string{"AGENTS.md"}},
		{"both present", []string{"CLAUDE.md", "AGENTS.md"}},
		{"case-insensitive", []string{"claude.md"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo := t.TempDir()
			owned := "# My instructions\n\nkeep me\n"
			for _, f := range c.files {
				if err := os.WriteFile(filepath.Join(repo, f), []byte(owned), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var out strings.Builder
			if err := runInitTest(t, &out, repo); err != nil {
				t.Fatalf("runInit: %v", err)
			}
			for _, f := range c.files {
				if !strings.Contains(out.String(), f) {
					t.Errorf("agent note does not name %s:\n%s", f, out.String())
				}
			}
			for _, want := range []string{"Agent note:", `"## satelle" section`, "satelle help", "prefer that over duplicating"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("agent note missing %q:\n%s", want, out.String())
				}
			}
			// AC1 (sty_fd4b1cd4): the note points custom/process agents at
			// .satelle/agents.toml, explicitly NOT a harness-specific agent dir.
			for _, want := range []string{"agents.toml", ".claude/agents"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("agent note missing the agents-layer pointer %q:\n%s", want, out.String())
				}
			}
			// AC4: the seeded agents.toml scaffold names the anti-pattern.
			toml, _ := os.ReadFile(filepath.Join(repo, ".satelle", "agents.toml"))
			for _, want := range []string{".claude/agents", "invisible", "one CLI vendor"} {
				if !strings.Contains(string(toml), want) {
					t.Errorf("seeded agents.toml missing anti-pattern comment %q:\n%s", want, toml)
				}
			}
			// init never edits the agent-owned file.
			for _, f := range c.files {
				if got, _ := os.ReadFile(filepath.Join(repo, f)); string(got) != owned {
					t.Errorf("init modified %s:\n%s", f, got)
				}
			}
		})
	}

	t.Run("neither present", func(t *testing.T) {
		repo := t.TempDir()
		var out strings.Builder
		if err := runInitTest(t, &out, repo); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Agent note:") {
			t.Errorf("agent note emitted with no instruction file present:\n%s", out.String())
		}
	})
}

// TestInitAgentsLayerValidatesZeroWarnings is the sty_5f1d7b2e regression guard:
// a fresh init deploys an agents.toml that agentvalidate accepts with ZERO
// warnings and ZERO problems. Any future scaffold-vs-validator drift (missing
// role=, role/path mismatch, in-loop reviewer, …) fails this test instead of
// shipping WARN lines to first-run users.
func TestInitAgentsLayerValidatesZeroWarnings(t *testing.T) {
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	// Init output itself must not emit agents-layer WARN lines (AC1 surface).
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "WARN") && strings.Contains(line, "agents.toml") {
			t.Errorf("fresh init emitted agents-layer WARN:\n%s", line)
		}
	}
	dataDir := filepath.Join(repo, config.DefaultDataDir)
	agents, err := config.LoadAgents(dataDir)
	if err != nil {
		t.Fatalf("LoadAgents after init: %v", err)
	}
	report := agentvalidate.Validate(agents, deployedVars(dataDir), deployedWorkflowDocs(dataDir))
	if len(report.Warnings) > 0 || len(report.Problems) > 0 {
		t.Fatalf("init-deployed agents layer must validate with zero warnings/problems:\n  warnings=%v\n  problems=%v",
			report.Warnings, report.Problems)
	}
	// AC4: re-init leaves an existing agents.toml untouched.
	path := filepath.Join(dataDir, config.AgentsConfigName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("re-init clobbered existing agents.toml")
	}
}

// TestRunInitMigratesDriftedAgentsToml: re-init format-migrates harness= → command=
// and adds missing role= (order:7), printing a ~ migrated line; post-migrate
// agentvalidate has zero WARN.
func TestRunInitMigratesDriftedAgentsToml(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	path := filepath.Join(repo, config.DefaultDataDir, config.AgentsConfigName)
	drifted := `[executor]
harness = "in-loop"

[reviewer]
harness = "claude"
tools   = "Read,Grep,Glob"
`
	if err := os.WriteFile(path, []byte(drifted), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("re-init: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "migrated:") || !strings.Contains(out.String(), "agents.toml") {
		t.Fatalf("want ~ migrated line for agents.toml, got:\n%s", out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	if strings.Contains(body, "harness") {
		t.Fatalf("harness not rewritten:\n%s", body)
	}
	if !strings.Contains(body, `command = "in-loop"`) || !strings.Contains(body, `role = "agent"`) {
		t.Fatalf("migration incomplete:\n%s", body)
	}
	if !strings.Contains(body, `role = "reviewer"`) {
		t.Fatalf("reviewer role missing:\n%s", body)
	}
	agents, err := config.LoadAgents(filepath.Join(repo, config.DefaultDataDir))
	if err != nil {
		t.Fatal(err)
	}
	report := agentvalidate.Validate(agents, deployedVars(filepath.Join(repo, config.DefaultDataDir)), deployedWorkflowDocs(filepath.Join(repo, config.DefaultDataDir)))
	for _, w := range report.Warnings {
		if strings.Contains(w, "role") || strings.Contains(w, "inferred") {
			t.Fatalf("role-inferred WARN after migrate: %v", report.Warnings)
		}
	}
	if !report.OK() {
		t.Fatalf("problems after migrate: %v", report.Problems)
	}
}

// TestRunInitRegistersWorkspace (sty_3bdbdc38 AC1): a fresh init leaves the repo
// in the local workspace registry that `satelle workspace list` reads.
func TestRunInitRegistersWorkspace(t *testing.T) {
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	abs, err := filepath.Abs(repo)
	if err != nil {
		t.Fatal(err)
	}
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range gc.Workspace.Repos {
		if r == abs {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("workspace registry missing %s: %v", abs, gc.Workspace.Repos)
	}
	if !strings.Contains(out.String(), "registered") {
		t.Errorf("init output missing registered line:\n%s", out.String())
	}
}

// TestRunInitWorkspaceIdempotent (AC2): re-init does not duplicate and does not fail.
func TestRunInitWorkspaceIdempotent(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("re-init: %v\n%s", err, out.String())
	}
	abs, _ := filepath.Abs(repo)
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range gc.Workspace.Repos {
		if r == abs {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly 1 registry entry for %s, got %d in %v", abs, n, gc.Workspace.Repos)
	}
	if !strings.Contains(out.String(), "already registered") {
		t.Errorf("re-init output missing already-registered line:\n%s", out.String())
	}
	if strings.Contains(out.String(), "  + workspace registry") {
		t.Errorf("re-init must not re-add (no + line):\n%s", out.String())
	}
}

// TestRunInitNoWorkspaceOptOut (AC3): --no-workspace skips registration.
func TestRunInitNoWorkspaceOptOut(t *testing.T) {
	isolateUserHome(t)
	repo := t.TempDir()
	var out strings.Builder
	if err := runInit(&out, repo, true); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	abs, _ := filepath.Abs(repo)
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range gc.Workspace.Repos {
		if r == abs {
			t.Fatalf("opt-out still registered %s in %v", abs, gc.Workspace.Repos)
		}
	}
	if !strings.Contains(out.String(), "skipped: --no-workspace") {
		t.Errorf("output missing skip line:\n%s", out.String())
	}
}

// TestRunInitWorkspaceWriteFailureNonFatal (AC4): unwritable global config warns
// but does not fail init. Runtime dir must still be creatable (home-keyed DB);
// only SaveGlobal's config.toml write is blocked (sty_4660bbe1).
func TestRunInitWorkspaceWriteFailureNonFatal(t *testing.T) {
	isolateUserHome(t)
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	// Make config.toml a directory so SaveGlobal's WriteFile fails while
	// MkdirAll(home/<repo-key>) for the runtime plane still succeeds.
	if err := os.MkdirAll(filepath.Join(home, config.GlobalConfigName), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	var out strings.Builder
	if err := runInit(&out, repo, false); err != nil {
		t.Fatalf("init must succeed despite unwritable global config: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "workspace registry") || !strings.Contains(out.String(), "!") {
		// Accept either "!" warn prefix or "skipped" wording.
		if !strings.Contains(out.String(), "registration skipped") && !strings.Contains(out.String(), "workspace registry (skipped") {
			t.Errorf("expected non-fatal workspace warning:\n%s", out.String())
		}
	}
}
