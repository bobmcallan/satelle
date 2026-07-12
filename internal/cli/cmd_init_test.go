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

	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/structure"
)

// isolateUserHome pins HOME to a disposable dir for this test so init's
// Grok-compat write (~/.grok/config.toml) cannot touch the developer's real
// config when grok is on PATH (sty_24b32127). Idempotent within a test.
func isolateUserHome(t *testing.T) {
	t.Helper()
	if os.Getenv("SATELLE_INIT_TEST_HOME") != "" {
		return
	}
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("SATELLE_INIT_TEST_HOME", h)
}

// runInitTest is runInit with HOME isolated (see isolateUserHome).
func runInitTest(t *testing.T, out io.Writer, repo string) error {
	t.Helper()
	isolateUserHome(t)
	return runInit(out, repo)
}

func TestRunInitScaffolds(t *testing.T) {
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Core files exist: the tomls, the db, a README per authored dir (incl.
	// stories), and the materialised reviewer skills the baseline references.
	for _, rel := range []string{
		".satelle/satelle.toml",
		".satelle/agents.toml",
		".satelle/satelle.db",
		".satelle/documents/README.md",
		".satelle/workflows/README.md",
		".satelle/principles/README.md",
		".satelle/skills/README.md",
		".satelle/skills/satelle-step-summary.md",
		".satelle/skills/satelle-workflow-advisor.md",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// Tasks: the dir + README keep-file are scaffolded, and the ONE embedded default
	// task — the re-runnable substrate-audit — is seeded (sty_d4360e90). No GENERIC
	// example task is seeded (sty_04ec1fe6): one named default, not example noise.
	if _, err := os.Stat(filepath.Join(repo, ".satelle/tasks/README.md")); err != nil {
		t.Errorf("init did not scaffold .satelle/tasks/README.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle/tasks/tsk_substrate-audit.md")); err != nil {
		t.Errorf("init did not seed the substrate-audit task: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle/tasks/tsk_example1.md")); err == nil {
		t.Error("init must not seed a generic example task (tsk_example1.md)")
	}

	// sty_bf153cbf reverses sty_3f9a6124: the baseline is now the seeded default
	// lifecycle on a fresh repo (no authored workflow claims "*" yet), so init
	// DOES write it as an editable repo file.
	if _, err := os.Stat(filepath.Join(repo, ".satelle/workflows/satelle-baseline-workflow.md")); err != nil {
		t.Errorf("init did not seed the baseline workflow as a repo file: %v", err)
	}
	// The removed .satelle/stories mirror must NOT be recreated (sty_746a0c98).
	if _, err := os.Stat(filepath.Join(repo, ".satelle/stories")); err == nil {
		t.Error("init must not scaffold .satelle/stories — the markdown mirror was removed")
	}

	// gitignore ignores the db but not the toml.
	gi, _ := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if !strings.Contains(string(gi), ".satelle/satelle.db") {
		t.Error("gitignore missing db entry")
	}
	if strings.Contains(string(gi), "\n.satelle/satelle.toml\n") {
		t.Error("gitignore should not ignore the committed toml")
	}

	// Report shows creations.
	if !strings.Contains(out.String(), "+ .satelle/satelle.db") {
		t.Errorf("report missing db creation:\n%s", out.String())
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
// ACTIVE (uncommented) [gate] edit_exempt_paths seeded with .satelle/ (sty_8c3d345c).
// Exemption is config, not code — a fresh repo keeps authored substrate editable via
// this seeded config rather than a hardcoded data-dir case in the binary.
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
	// The [gate] table and edit_exempt_paths must be ACTIVE (no leading '#') and seed .satelle/.
	for _, want := range []string{"\n[gate]\n", "edit_exempt_paths = [\".satelle/\"]"} {
		if !strings.Contains(tomlSrc, want) {
			t.Errorf("scaffold satelle.toml missing active %q:\n%s", want, tomlSrc)
		}
	}
	// Parse it to confirm the seeded value actually resolves as an exempt prefix.
	var cfg config.Config
	if _, err := toml.Decode(tomlSrc, &cfg); err != nil {
		t.Fatalf("scaffold satelle.toml does not parse: %v", err)
	}
	if got := cfg.ResolveEditExemptPaths(repo); len(got) != 1 || !strings.HasSuffix(got[0], ".satelle") {
		t.Errorf("ResolveEditExemptPaths = %v, want one entry ending .satelle", got)
	}
}

// TestInstallAliasSharesInitPath: satelle install is a cobra alias of init —
// one RunE, two names (sty_0e268c9a). Resolves the registered command tree.
func TestInstallAliasSharesInitPath(t *testing.T) {
	root := NewRootCmd()
	initCmd, _, err := root.Find([]string{"init"})
	if err != nil {
		t.Fatalf("find init: %v", err)
	}
	installCmd, _, err := root.Find([]string{"install"})
	if err != nil {
		t.Fatalf("find install: %v", err)
	}
	if initCmd != installCmd {
		t.Fatalf("install must resolve to the same command as init (one code path)")
	}
	found := false
	for _, a := range initCmd.Aliases {
		if a == "install" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("init Aliases missing install: %v", initCmd.Aliases)
	}
	// Functional: install-named path still scaffolds (same RunE).
	repo := t.TempDir()
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit via install path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".satelle", "agents.toml")); err != nil {
		t.Errorf("install/init did not scaffold agents.toml: %v", err)
	}
}

// TestRunInitSeedsAdvisorySkillsBesideAuthoredWorkflows: advisory skills are
// workflow-independent guidance, so they seed even when the default solution is
// withheld because the repo authored its own workflow set (sty_f4c1bd90).
func TestRunInitSeedsAdvisorySkillsBesideAuthoredWorkflows(t *testing.T) {
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
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("advisor skill not seeded beside an authored workflow set: %v", err)
	}
	// And it is valid substrate.
	if problems := structure.Doc("skills", "satelle-workflow-advisor", string(body), nil); len(problems) > 0 {
		t.Errorf("seeded advisor skill fails its structure check: %v", problems)
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

// TestRunInitSeedsAuditTask asserts the ONE embedded default task — the
// re-runnable substrate-audit — lands in a fresh repo READY TO RUN (AC4,
// sty_d4360e90): structurally clean (CheckTask), sitting at `done`, resolving to
// the task workflow by its kind category, and a new execution targeting it as
// parent passes the CODED begin-run gate. That gate judges STRUCTURE only (never
// the parent's status), so a done header accepts a fresh run each time —
// "re-runnable from done".
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
		t.Errorf("seeded audit task must sit at status: done (re-runnable from done), got:\n%s", body)
	}

	// Resolves to the task workflow by its kind category (applies_to ["execution","task"]).
	var docs []docindex.Doc
	for _, wf := range defaultSolutionWorkflows {
		b, err := os.ReadFile(filepath.Join(dataDir, "workflows", wf+".md"))
		if err != nil {
			t.Fatalf("read seeded %s: %v", wf, err)
		}
		docs = append(docs, docindex.Doc{Name: wf, Body: string(b)})
	}
	ordered := agentstep.OrderedWorkflows(docs, "task")
	if len(ordered) == 0 || ordered[0].Name != "satelle-task-workflow" {
		t.Errorf("audit task does not resolve to satelle-task-workflow: %+v", ordered)
	}

	// Re-runnable from done: run the REAL coded begin-run gate (extracted from the
	// seeded skill) over a new execution whose parent is the done audit task. The
	// gate never reads the parent's status, so done does not block a new run.
	skillBody, err := os.ReadFile(filepath.Join(dataDir, "skills", "satelle-task-validate-before-review.md"))
	if err != nil {
		t.Fatalf("seeded task-validate-before skill missing: %v", err)
	}
	payload := `{"story":{"id":"exe_test0001","kind":"execution","title":"audit run","status":"backlog","parent_id":"tsk_substrate-audit","tags":[],"created_at":"2026-07-08T00:00:00Z","updated_at":"2026-07-08T00:00:00Z"},"from":"backlog","to":"in_progress","review_skill":"satelle-task-validate-before-review"}`
	cmd := exec.Command("sh", "-c", checkScript(t, string(skillBody)))
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

// TestRunInitSeedsDefaultSolution asserts a fresh init deploys the COMPLETE
// default solution (sty_a7cbd6dd, reversed to seed the base by sty_bf153cbf):
// the generic base/parent/task-execution workflows plus every gate skill they
// reference — and that the seeded set is structure-conformant and consistent
// (what `satelle workflow validate` checks: no dangling refs, no ambiguous
// applies_to).
func TestRunInitSeedsDefaultSolution(t *testing.T) {
	repo := t.TempDir()
	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	dataDir := filepath.Join(repo, ".satelle")

	for _, wf := range defaultSolutionWorkflows {
		if !fileExists(filepath.Join(dataDir, "workflows", wf+".md")) {
			t.Errorf("init did not seed workflows/%s.md", wf)
		}
	}
	for _, sk := range defaultSolutionSkills {
		if !fileExists(filepath.Join(dataDir, "skills", sk+".md")) {
			t.Errorf("init did not seed skills/%s.md", sk)
		}
	}

	// The seeded set validates: every file passes the deterministic structure
	// check, and the workflow set is consistent with every referenced skill
	// resolving on disk.
	resolve := func(skill string) bool {
		return fileExists(filepath.Join(dataDir, "skills", skill+".md"))
	}
	var docs []docindex.Doc
	for _, wf := range defaultSolutionWorkflows {
		body, err := os.ReadFile(filepath.Join(dataDir, "workflows", wf+".md"))
		if err != nil {
			t.Fatalf("read seeded %s: %v", wf, err)
		}
		for _, p := range structure.Doc("workflows", wf, string(body), resolve) {
			t.Errorf("seeded workflows/%s: %s", wf, p)
		}
		docs = append(docs, docindex.Doc{Name: wf, Body: string(body)})
	}
	for _, p := range agentstep.WorkflowConsistency(docs, resolve) {
		t.Errorf("seeded workflow set inconsistent: %s", p)
	}
	for _, sk := range defaultSolutionSkills {
		body, err := os.ReadFile(filepath.Join(dataDir, "skills", sk+".md"))
		if err != nil {
			t.Fatalf("read seeded %s: %v", sk, err)
		}
		for _, p := range structure.Doc("skills", sk, string(body), nil) {
			t.Errorf("seeded skills/%s: %s", sk, p)
		}
	}

	// An execution resolves to the task-execution workflow out of the box: the
	// kind-aware category ("execution") selects it ahead of the wildcard.
	ordered := agentstep.OrderedWorkflows(docs, "execution")
	if len(ordered) == 0 || ordered[0].Name != "satelle-task-workflow" {
		t.Errorf("execution does not resolve to satelle-task-workflow: %+v", ordered)
	}

	// The generic base default is the MINIMAL order-zero lifecycle (sty_bf153cbf):
	// no release mechanics beyond backlog/in_progress/done/cancelled, and its
	// declared gates are exactly the story intent/done/cancel reviews plus the
	// coded estimate check and the step summary.
	baseBody, _ := os.ReadFile(filepath.Join(dataDir, "workflows", "satelle-baseline-workflow.md"))
	for _, state := range []string{"commit", "push", "committed", "integration"} {
		if strings.Contains(string(baseBody), state+" [") || strings.Contains(string(baseBody), state+"  [") {
			t.Errorf("generic base workflow declares extra state %q", state)
		}
	}
	for _, gate := range []string{"satelle-story-intent-review", "satelle-story-done-review", "satelle-story-cancel-review", "satelle-estimate-actual-review"} {
		if !strings.Contains(string(baseBody), gate) {
			t.Errorf("generic base workflow must reference gate %q", gate)
		}
	}
	if strings.Contains(string(baseBody), "satelle-code-ac-review") {
		t.Error("generic base workflow must not reference satelle-code-ac-review")
	}
	// The estimate gate it declares is CODED — the seeded skill carries a
	// self-contained check block, so no agent CLI is needed for it.
	estBody, _ := os.ReadFile(filepath.Join(dataDir, "skills", "satelle-estimate-actual-review.md"))
	if !strings.Contains(string(estBody), "```check") {
		t.Error("seeded estimate skill must carry a self-contained ```check block")
	}
	// The embedded code-ac reviewer was removed with the gates (sty_f804caaa).
	if fileExists(filepath.Join(dataDir, "skills", "satelle-code-ac-review.md")) {
		t.Error("init must not seed satelle-code-ac-review — no seeded workflow references it")
	}
}

// TestRunInitSeedsAdditivelyBesideAuthoredWorkflow asserts init seeds the
// default solution ADDITIVELY, per file (sty_f6bd6f84): beside an authored
// wildcard project workflow, the wildcard default is SKIPPED (routing safety —
// it would duplicate the "*" precedence), but the non-overlapping parent and
// task-execution defaults DO seed, the authored file is untouched, and the
// deployed system still validates.
func TestRunInitSeedsAdditivelyBesideAuthoredWorkflow(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A CONFORMANT authored wildcard project workflow — init validates the
	// deployed system, so the healed set must still validate green.
	own := filepath.Join(wfDir, "my-workflow.md")
	ownBody := "---\nname: my-workflow\ntype: workflow\ndescription: my own lifecycle\napplies_to: [\"*\"]\nscope: project\n---\n\n# mine\n\n```dot\ndigraph w {\n  backlog -> in_progress -> done\n}\n```\n"
	if err := os.WriteFile(own, []byte(ownBody), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	// The wildcard base default is skipped (it would compete with the authored
	// "*" workflow); the report explains why.
	if fileExists(filepath.Join(wfDir, "satelle-baseline-workflow.md")) {
		t.Error("init seeded the wildcard base default beside an authored wildcard workflow")
	}
	if !strings.Contains(out.String(), "claimed by an authored workflow") {
		t.Errorf("report does not explain the skipped wildcard default:\n%s", out.String())
	}
	// The non-overlapping defaults DO seed — this is the additive heal.
	for _, wf := range []string{"satelle-parent-workflow", "satelle-task-workflow"} {
		if !fileExists(filepath.Join(wfDir, wf+".md")) {
			t.Errorf("init did not additively seed %s beside the authored workflow", wf)
		}
	}
	// The gate skills the defaults reference are seeded even though the base
	// default's own file was skipped (its refs are still collected).
	for _, sk := range defaultSolutionSkills {
		if !fileExists(filepath.Join(repo, ".satelle", "skills", sk+".md")) {
			t.Errorf("init did not seed referenced gate skill %s", sk)
		}
	}
	if got, _ := os.ReadFile(own); !strings.Contains(string(got), "# mine") {
		t.Error("authored workflow was modified")
	}
}

// TestRunInitHealsMissingGateSkillDeadlock reproduces the satelle-server field
// failure (sty_f6bd6f84): a repo authored its own wildcard project workflow and,
// under an older binary, had some gate skills seeded but not
// satelle-estimate-actual-review — so the current binary's fail-fast validation
// would refuse over the dangling reference while holding the file embedded.
// Per-file additive seeding must HEAL it: re-running init seeds the missing gate
// skill (the default solution references it), leaves the present files untouched,
// and validates green. A second init is idempotent.
func TestRunInitHealsMissingGateSkillDeadlock(t *testing.T) {
	repo := t.TempDir()
	dataDir := filepath.Join(repo, ".satelle")
	wfDir := filepath.Join(dataDir, "workflows")
	skDir := filepath.Join(dataDir, "skills")
	for _, d := range []string{wfDir, skDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Authored wildcard project workflow whose estimate gate references the gate
	// skill that is MISSING on disk — the dangling reference the old init deadlocked on.
	own := "---\nname: my-workflow\ntype: workflow\ndescription: my own lifecycle\napplies_to: [\"*\"]\nscope: project\n---\n\n# mine\n\n```dot\ndigraph w {\n  backlog -> in_progress -> done\n  estimate [agent=reviewer, prompt=\"@skill:satelle-estimate-actual-review\", on=\"in_progress,done\"]\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "my-workflow.md"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	// satelle-estimate-actual-review is intentionally absent; init must seed it.
	if fileExists(filepath.Join(skDir, "satelle-estimate-actual-review.md")) {
		t.Fatal("precondition: the gate skill must start absent")
	}

	if err := runInitTest(t, io.Discard, repo); err != nil {
		t.Fatalf("init must HEAL the missing default and validate green, got: %v", err)
	}
	if !fileExists(filepath.Join(skDir, "satelle-estimate-actual-review.md")) {
		t.Error("init did not seed the missing gate skill — deadlock not healed")
	}
	// The authored workflow is untouched.
	if got, _ := os.ReadFile(filepath.Join(wfDir, "my-workflow.md")); string(got) != own {
		t.Error("init modified the authored workflow while healing")
	}
	// Idempotent: a second init creates nothing new.
	var out strings.Builder
	if err := runInitTest(t, &out, repo); err != nil {
		t.Fatalf("second init: %v", err)
	}
	if strings.Contains(out.String(), "  + ") {
		t.Errorf("second init created something (not idempotent):\n%s", out.String())
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
}

func TestEnsureClaudeHooksIdempotent(t *testing.T) {
	repo := t.TempDir()
	created, _, err := ensureClaudeHooks(repo)
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v, want created", created, err)
	}
	path := filepath.Join(repo, ".claude", "settings.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("settings not written: %v", err)
	}
	for _, want := range []string{
		"PATH=$HOME/.local/bin:$PATH satelle hook gate || exit 2",
		"PATH=$HOME/.local/bin:$PATH satelle hook commitgate || exit 2",
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
	// An existing file that LACKS the reinforcement hooks is HEALED, not left
	// ungated (sty_949e8739): prompt/stopcheck are appended and other keys are
	// preserved — otherwise a repo initialized before the reinforcement hooks
	// shipped would never gain them, re-opening the bypass.
	if err := os.WriteFile(path, []byte("{\"custom\":true}"), 0o644); err != nil {
		t.Fatal(err)
	}
	created2, updated2, err := ensureClaudeHooks(repo)
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
	created3, updated3, err := ensureClaudeHooks(repo)
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
