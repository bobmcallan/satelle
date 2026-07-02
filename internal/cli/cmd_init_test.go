package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
