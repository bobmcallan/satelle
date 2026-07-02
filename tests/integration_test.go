//go:build integration

// Package tests holds satelle's black-box integration tests: they build the
// real binary and drive it end-to-end through the dogfood flow (init → story →
// index → status → serve), asserting on actual process output. Gated behind the
// `integration` build tag so the default `go test ./...` stays fast and
// hermetic; run with:
//
//	go test -tags integration ./tests/...
package tests

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testBin is the satelle binary under test, resolved once by TestMain.
var testBin string

// TestMain resolves the binary the suite drives. If SATELLE_BIN points at an
// existing binary it is used as-is — so the suite can run against a prebuilt or
// installed binary without a rebuild:
//
//	cd tests && SATELLE_BIN=$(command -v satelle) go test -tags integration .
//
// Otherwise satelle is built once into a temp dir shared across all tests.
func TestMain(m *testing.M) {
	if env := os.Getenv("SATELLE_BIN"); env != "" {
		abs, err := filepath.Abs(env)
		if err != nil || !fileExists(abs) {
			fmt.Fprintf(os.Stderr, "SATELLE_BIN=%q not usable: %v\n", env, err)
			os.Exit(1)
		}
		testBin = abs
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "satelle-itest")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdtemp:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)
	testBin = filepath.Join(dir, "satelle")
	// The test runs from tests/, so the module root is one level up.
	build := exec.Command("go", "build", "-o", testBin, "./cmd/satelle")
	build.Dir = ".."
	if out, berr := build.CombinedOutput(); berr != nil {
		fmt.Fprintf(os.Stderr, "build satelle: %v\n%s", berr, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// stubReviewerAccept points the repo's reviewer binding at a deterministic accept
// script, so the now-active baseline gates (materialised by init, sty_5b8bd8b2) do
// not invoke a real agent CLI in hermetic tests. Call after init, before any status
// transition. Gate CONTENT is covered separately (create_review, baseline_skills).
func stubReviewerAccept(t *testing.T, repo string) {
	t.Helper()
	verdict := filepath.Join(repo, "verdict-accept.sh")
	if err := os.WriteFile(verdict, []byte("#!/bin/sh\necho '{\"decision\":\"accept\",\"notes\":\"\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"),
		[]byte(fmt.Sprintf("[reviewer]\nharness = \"%s {system} {tools} {model}\"\n", verdict)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run executes the binary in dir with args and returns combined output.
func run(t *testing.T, bin, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustRun fails the test if the command errors.
func mustRun(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()
	out, err := run(t, bin, dir, args...)
	if err != nil {
		t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func TestDogfoodFlow(t *testing.T) {
	bin := testBin
	repo := t.TempDir()

	// init scaffolds the repo.
	out := mustRun(t, bin, repo, "init")
	for _, want := range []string{".satelle/satelle.toml", ".satelle/satelle.db", "+ .gitignore"} {
		if !strings.Contains(out, want) {
			t.Errorf("init output missing %q:\n%s", want, out)
		}
	}
	for _, rel := range []string{".satelle/satelle.toml", ".satelle/satelle.db", ".satelle/workflows/README.md"} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not create %s", rel)
		}
	}

	// init is idempotent.
	out = mustRun(t, bin, repo, "init")
	if strings.Contains(out, "  + ") {
		t.Errorf("second init created something:\n%s", out)
	}

	// The materialised baseline gates are now active; stub the reviewer so the
	// status transitions below stay hermetic (sty_5b8bd8b2).
	stubReviewerAccept(t, repo)

	// Index the substrate init materialised (baseline workflow + step skill), as a
	// real session does at SessionStart, so a later authoring index is incremental.
	mustRun(t, bin, repo, "reindex")

	// Create a story and a task.
	out = mustRun(t, bin, repo, "story", "create", "--title", "Dogfood satelle", "--priority", "high", "--tags", "mvp,core")
	if !strings.Contains(out, `"sty_`) || !strings.Contains(out, "Dogfood satelle") {
		t.Fatalf("story create output:\n%s", out)
	}
	storyID := extractID(out, "sty_")
	mustRun(t, bin, repo, "task", "create", "--title", "write release notes")

	// The seeded default's CODED estimate gate enforces out of the box
	// (sty_f804caaa): begin-work without an estimate is rejected deterministically.
	if out, err := run(t, bin, repo, "story", "set", storyID, "--status", "in_progress"); err == nil || !strings.Contains(out, "no plan estimate recorded") {
		t.Fatalf("begin-work without an estimate should be rejected by the coded gate: err=%v\n%s", err, out)
	}
	mustRun(t, bin, repo, "story", "estimate", storyID, "--time", "10m")

	// Move the story along the seeded default workflow.
	out = mustRun(t, bin, repo, "story", "set", storyID, "--status", "in_progress")
	if !strings.Contains(out, `"status": "in_progress"`) {
		t.Errorf("story set status:\n%s", out)
	}

	// Lifecycle events landed in the ledger.
	out = mustRun(t, bin, repo, "ledger", "list", "--story", storyID)
	if !strings.Contains(out, "story_created") || !strings.Contains(out, "status_transition") {
		t.Errorf("ledger missing lifecycle events:\n%s", out)
	}

	// Author markdown and index it.
	docsDir := filepath.Join(repo, ".satelle", "documents")
	if err := os.WriteFile(filepath.Join(docsDir, "onboarding.md"), []byte("# Onboarding\n\nhi"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = mustRun(t, bin, repo, "reindex")
	if !strings.Contains(out, `"indexed": 1`) {
		t.Errorf("index output:\n%s", out)
	}
	out = mustRun(t, bin, repo, "doc", "get", "documents", "onboarding")
	if !strings.Contains(out, `"headline": "Onboarding"`) {
		t.Errorf("doc get headline:\n%s", out)
	}

	// status reflects the counts.
	out = mustRun(t, bin, repo, "status")
	for _, want := range []string{"stories", "tasks", "indexed documents   1"} {
		if !strings.Contains(out, want) {
			t.Errorf("status missing %q:\n%s", want, out)
		}
	}
}

func TestServeServesProjectPage(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	mustRun(t, bin, repo, "story", "create", "--title", "Render me")

	const port = "8791"
	cmd := exec.Command(bin, "serve", "--port", port)
	cmd.Dir = repo
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	base := "http://127.0.0.1:" + port
	if !waitHealthy(t, base+"/healthz", 5*time.Second) {
		t.Fatal("server did not become healthy")
	}

	slug := filepath.Base(repo)

	// / is the connected-projects landing — it lists this lone repo at its slug,
	// not the repo's project page directly.
	landing := httpGet(t, base+"/")
	for _, want := range []string{"in the workspace", `href="/` + slug + `/#stories"`, "satelle"} {
		if !strings.Contains(landing, want) {
			t.Errorf("landing missing %q:\n%s", want, landing)
		}
	}

	// The project page itself is served under the repo's slug.
	body := httpGet(t, base+"/"+slug+"/")
	for _, want := range []string{"Render me", "Stories", "Tasks", "satelle"} {
		if !strings.Contains(body, want) {
			t.Errorf("project page missing %q", want)
		}
	}
	if code := httpStatus(t, base+"/nope"); code != 404 {
		t.Errorf("unknown path = %d, want 404", code)
	}
}

// --- helpers ---

func extractID(out, prefix string) string {
	i := strings.Index(out, prefix)
	if i < 0 {
		return ""
	}
	rest := out[i:]
	end := strings.IndexAny(rest, `"`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func waitHealthy(t *testing.T, url string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func httpGet(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		b.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	return b.String()
}

func httpStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestInitDeploysDefaultSolution asserts a fresh init lands the COMPLETE default
// solution (sty_a7cbd6dd): the generic project/parent/task-execution workflows
// plus every referenced gate skill, validating green with no dangling refs, and
// an execution resolving to the task-execution workflow out of the box.
func TestInitDeploysDefaultSolution(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")

	for _, rel := range []string{
		".satelle/workflows/satelle-project-workflow.md",
		".satelle/workflows/satelle-parent-workflow.md",
		".satelle/workflows/satelle-task-workflow.md",
		".satelle/skills/satelle-estimate-actual-review.md",
		".satelle/skills/satelle-task-validate-before-review.md",
		".satelle/skills/satelle-task-validate-after-review.md",
		".satelle/skills/satelle-story-intent-review.md",
		".satelle/skills/satelle-story-done-review.md",
		".satelle/skills/satelle-story-cancel-review.md",
		".satelle/skills/satelle-step-summary.md",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Errorf("init did not seed %s", rel)
		}
	}

	// Index the seeded substrate (as a session does at SessionStart), then the
	// per-noun validators must pass out of the box.
	mustRun(t, bin, repo, "reindex")
	mustRun(t, bin, repo, "workflow", "validate")
	mustRun(t, bin, repo, "skill", "validate")

	// An execution resolves to the task-execution workflow, not the wildcard: the
	// head (active) entry of the ordered list for the execution kind-category.
	out := mustRun(t, bin, repo, "workflow", "list", "--category", "execution")
	firstObj := out
	if i := strings.Index(out, "}"); i >= 0 {
		firstObj = out[:i]
	}
	if !strings.Contains(firstObj, `"name": "satelle-task-workflow"`) {
		t.Errorf("head workflow for an execution is not satelle-task-workflow:\n%s", out)
	}

	// And a run can be created against the seeded starter task immediately.
	out = mustRun(t, bin, repo, "execution", "create", "--parent", "tsk_example1", "--title", "run 1")
	if !strings.Contains(out, `"exe_`) {
		t.Fatalf("execution create output:\n%s", out)
	}
}

// TestRebaseResetsSubstrate asserts `satelle rebase` aborts without confirmation,
// and with --yes backs up the customized substrate to a timestamped dir, wipes
// it, and redeploys the complete default solution (sty_a7cbd6dd).
func TestRebaseResetsSubstrate(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")

	// Customize: drift a seeded skill, add an extra authored workflow.
	skill := filepath.Join(repo, ".satelle", "skills", "satelle-code-ac-review.md")
	if err := os.WriteFile(skill, []byte("# drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(repo, ".satelle", "workflows", "extra-workflow.md")
	if err := os.WriteFile(extra, []byte("# extra\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No confirmation (empty stdin) → abort, nothing changed.
	out, err := run(t, bin, repo, "rebase")
	if err != nil || !strings.Contains(out, "aborted") {
		t.Fatalf("unconfirmed rebase: err=%v\n%s", err, out)
	}
	if _, serr := os.Stat(extra); serr != nil {
		t.Error("unconfirmed rebase removed the extra workflow")
	}

	// Confirmed rebase: backup + wipe + redeploy.
	out = mustRun(t, bin, repo, "rebase", "--yes")
	if !strings.Contains(out, "backed up") || !strings.Contains(out, "deployed") {
		t.Errorf("rebase report incomplete:\n%s", out)
	}
	if b, _ := os.ReadFile(skill); strings.Contains(string(b), "# drifted") {
		t.Error("rebase did not reset the drifted skill to the embedded default")
	}
	if _, serr := os.Stat(extra); serr == nil {
		t.Error("rebase left the extra authored workflow in the live dir")
	}
	if _, serr := os.Stat(filepath.Join(repo, ".satelle", "workflows", "satelle-project-workflow.md")); serr != nil {
		t.Error("rebase did not redeploy the default project workflow")
	}

	// The backup holds the pre-rebase files.
	backups := filepath.Join(repo, ".satelle", "backups")
	entries, rerr := os.ReadDir(backups)
	if rerr != nil || len(entries) != 1 {
		t.Fatalf("expected one timestamped backup dir: %v %v", entries, rerr)
	}
	backupDir := filepath.Join(backups, entries[0].Name())
	if _, serr := os.Stat(filepath.Join(backupDir, "workflows", "extra-workflow.md")); serr != nil {
		t.Error("backup missing the extra authored workflow")
	}
	if b, _ := os.ReadFile(filepath.Join(backupDir, "skills", "satelle-code-ac-review.md")); !strings.Contains(string(b), "# drifted") {
		t.Error("backup missing the drifted skill bytes")
	}
}

// TestStoryRestamp exercises the first-class re-stamp (sty_ed3386cf): a story
// stamped at create picks up a category-specific workflow authored later — the
// re-categorise → restamp flow — with the change ledgered; an invalid target is
// rejected without touching the stamp.
func TestStoryRestamp(t *testing.T) {
	bin := testBin
	repo := t.TempDir()
	mustRun(t, bin, repo, "init")
	mustRun(t, bin, repo, "reindex")

	// A feature story stamps the seeded wildcard project workflow at create.
	out := mustRun(t, bin, repo, "story", "create", "--title", "Assess the rollout", "--category", "feature")
	if !strings.Contains(out, `"workflow:satelle-project-workflow"`) {
		t.Fatalf("create did not stamp the seeded project workflow:\n%s", out)
	}
	id := extractID(out, "sty_")

	// A category-specific governance workflow is authored AFTER create.
	gov := `---
name: gov-workflow
scope: project
type: workflow
tags: [type:workflow]
applies_to: ["governance"]
description: Governance lifecycle moving backlog → in_progress → done with a cancelled exit.
---

# governance workflow

` + "```dot\n" + `digraph w {
  backlog [shape=Mdiamond]
  in_progress [agent=executor]
  done [shape=Msquare, agent=reviewer, prompt="@skill:satelle-story-done-review"]
  cancelled [agent=reviewer, prompt="@skill:satelle-story-cancel-review"]
  backlog -> in_progress
  in_progress -> done
  backlog -> cancelled
  in_progress -> cancelled
}` + "\n```\n"
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "gov-workflow.md"), gov)
	mustRun(t, bin, repo, "reindex")

	// Re-categorise, then restamp re-resolves from the CURRENT category.
	mustRun(t, bin, repo, "story", "set", id, "--category", "governance")
	out = mustRun(t, bin, repo, "story", "restamp", id)
	if !strings.Contains(out, `"workflow:gov-workflow"`) || strings.Contains(out, `"workflow:satelle-project-workflow"`) {
		t.Fatalf("restamp did not swap the governing workflow:\n%s", out)
	}

	// The trail records old -> new. The ledger JSON escapes ">" (>), so
	// match the escaped body as printed.
	out = mustRun(t, bin, repo, "ledger", "list", "--story", id)
	if !strings.Contains(out, "re-stamped: satelle-project-workflow -\\u003e gov-workflow") {
		t.Errorf("ledger missing the re-stamp row:\n%s", out)
	}

	// An unknown target is rejected and the stamp is untouched.
	if out, err := run(t, bin, repo, "story", "restamp", id, "--workflow", "nope"); err == nil || !strings.Contains(out, "does not resolve") {
		t.Errorf("unknown-workflow restamp should fail: err=%v\n%s", err, out)
	}
	out = mustRun(t, bin, repo, "story", "get", id)
	if !strings.Contains(out, `"workflow:gov-workflow"`) {
		t.Errorf("stamp changed after a rejected restamp:\n%s", out)
	}
}
