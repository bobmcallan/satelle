package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/reviewer"
	"github.com/bobmcallan/satelle/internal/structure"
)

func TestRunInitScaffolds(t *testing.T) {
	repo := t.TempDir()
	var out strings.Builder
	if err := runInit(&out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	// Core files exist: the tomls, the db, a README per authored dir (incl.
	// stories), and the materialised reviewer skills the baseline references. The
	// baseline WORKFLOW itself is embedded-only — never a repo file (sty_3f9a6124).
	for _, rel := range []string{
		".satelle/satelle.toml",
		".satelle/agents.toml",
		".satelle/satelle.db",
		".satelle/documents/README.md",
		".satelle/workflows/README.md",
		".satelle/principles/README.md",
		".satelle/skills/README.md",
		".satelle/skills/satelle-step-summary.md",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	// Tasks are scaffolded: the dir + README keep-file and the seeded starter task
	// header (sty_c1b3b4e3).
	for _, rel := range []string{".satelle/tasks/README.md", ".satelle/tasks/tsk_example1.md"} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not seed %s: %v", rel, err)
		}
	}

	// The baseline workflow must NOT be scaffolded as a repo file (embedded-only).
	if _, err := os.Stat(filepath.Join(repo, ".satelle/workflows/satelle-baseline-workflow.md")); err == nil {
		t.Error("init must not write the baseline workflow as a repo file — it is embedded-only")
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
}

func TestRunInitIdempotent(t *testing.T) {
	repo := t.TempDir()
	if err := runInit(io.Discard, repo); err != nil {
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
	if err := runInit(&out, repo); err != nil {
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

	// A user edit to the seeded task also survives re-init (never clobbered).
	taskPath := filepath.Join(repo, ".satelle", "tasks", "tsk_example1.md")
	edited := "---\nid: tsk_example1\ntype: task\nstatus: in_progress\n---\n\n# Mine\n\nACTION; VERIFICATION.\n"
	if err := os.WriteFile(taskPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInit(io.Discard, repo); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(taskPath); string(got) != edited {
		t.Errorf("re-init clobbered the authored task:\n%s", got)
	}
}

// defaultSolutionSkills is every gate/executor skill the seeded default solution
// references — the set a fresh repo must hold on disk so nothing dangles. (The
// story reviewers seed via the parent workflow and the embedded baseline
// fallback; the seeded PROJECT default itself is gateless apart from the coded
// estimate check and the step summary, sty_f804caaa.)
var defaultSolutionSkills = []string{
	"satelle-estimate-actual-review",
	"satelle-step-summary",
	"satelle-story-cancel-review",
	"satelle-story-done-review",
	"satelle-story-intent-review",
	"satelle-task-validate-before-review",
	"satelle-task-validate-after-review",
}

// TestRunInitSeedsDefaultSolution asserts a fresh init deploys the COMPLETE
// default solution (sty_a7cbd6dd): the generic project/parent/task-execution
// workflows plus every gate skill they reference — and that the seeded set is
// structure-conformant and consistent (what `satelle workflow validate` checks:
// no dangling refs, no ambiguous applies_to).
func TestRunInitSeedsDefaultSolution(t *testing.T) {
	repo := t.TempDir()
	if err := runInit(io.Discard, repo); err != nil {
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
	for _, p := range reviewer.WorkflowConsistency(docs, resolve) {
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
	ordered := reviewer.OrderedWorkflows(docs, "execution")
	if len(ordered) == 0 || ordered[0].Name != "satelle-task-workflow" {
		t.Errorf("execution does not resolve to satelle-task-workflow: %+v", ordered)
	}

	// The generic project default is the MOST BASIC lifecycle (sty_f804caaa): no
	// release mechanics, no reviewer gates — no reviewer_skill on any edge and no
	// gate prompt beyond the coded estimate check and the step summary.
	projBody, _ := os.ReadFile(filepath.Join(dataDir, "workflows", "satelle-project-workflow.md"))
	for _, state := range []string{"commit", "push", "committed", "integration"} {
		if strings.Contains(string(projBody), state+" [") || strings.Contains(string(projBody), state+"  [") {
			t.Errorf("generic project workflow declares extra state %q", state)
		}
	}
	if strings.Contains(string(projBody), "reviewer_skill") {
		t.Error("generic project workflow must carry no edge reviewers")
	}
	for _, gate := range []string{"satelle-story-intent-review", "satelle-code-ac-review", "satelle-story-done-review", "satelle-story-cancel-review"} {
		if strings.Contains(string(projBody), gate) {
			t.Errorf("generic project workflow must not reference reviewer %q", gate)
		}
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

// TestRunInitRespectsAuthoredWorkflows asserts init never seeds the default
// solution beside an existing authored workflow set (it would compete with it).
func TestRunInitRespectsAuthoredWorkflows(t *testing.T) {
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(wfDir, "my-workflow.md")
	if err := os.WriteFile(own, []byte("---\nname: my-workflow\n---\n\n# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runInit(&out, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, wf := range defaultSolutionWorkflows {
		if fileExists(filepath.Join(wfDir, wf+".md")) {
			t.Errorf("init seeded %s beside an authored workflow set", wf)
		}
	}
	if !strings.Contains(out.String(), "authored workflows present") {
		t.Errorf("report does not say the authored set was respected:\n%s", out.String())
	}
	if got, _ := os.ReadFile(own); !strings.Contains(string(got), "# mine") {
		t.Error("authored workflow was modified")
	}
}

// TestStarterTaskIsValid asserts the seeded starter task header passes the
// deterministic task structure check — a fresh repo's example is valid substrate
// (sty_c1b3b4e3).
func TestStarterTaskIsValid(t *testing.T) {
	if p := structure.CheckTask(scaffoldStarterTask); len(p) != 0 {
		t.Errorf("seeded starter task fails CheckTask: %v", p)
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
	for _, want := range []string{"satelle hook gate || exit 2", "satelle hook commitgate || exit 2", "satelle hook context", "Edit|Write"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("settings.json missing %q", want)
		}
	}
	// Second call must NOT overwrite (idempotent).
	if err := os.WriteFile(path, []byte("{\"custom\":true}"), 0o644); err != nil {
		t.Fatal(err)
	}
	created2, _, err := ensureClaudeHooks(repo)
	if err != nil || created2 {
		t.Fatalf("second call: created=%v err=%v, want not created", created2, err)
	}
	b2, _ := os.ReadFile(path)
	if string(b2) != "{\"custom\":true}" {
		t.Errorf("ensureClaudeHooks overwrote an existing settings.json")
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
			if err := runInit(&out, repo); err != nil {
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
		if err := runInit(&out, repo); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "Agent note:") {
			t.Errorf("agent note emitted with no instruction file present:\n%s", out.String())
		}
	})
}
