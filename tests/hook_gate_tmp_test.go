//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHookGateTmpDraftAllowed (sty_e8e1879c AC1/AC4): after init, an out-of-tree
// /tmp draft is allowed with no performing story on both harness envelopes;
// in-repo product code stays denied even when the repo itself lives under /tmp.
func TestHookGateTmpDraftAllowed(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")

	draft := filepath.Join(os.TempDir(), "itest-epic-buildout", "c1-title.txt")
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	// The draft path must sit outside the test repo (repo is also under TempDir).
	if filepath.Dir(draft) == repo || filepath.Dir(filepath.Dir(draft)) == repo {
		t.Fatalf("draft %q must not live inside repo %q", draft, repo)
	}

	if !gateEvent(t, repo, draft) {
		t.Errorf("edit gate blocked out-of-tree %s (Claude envelope)", draft)
	}
	if !gateEventRaw(t, repo, `{"toolInput":{"file_path":"`+draft+`"}}`) {
		t.Errorf("edit gate blocked out-of-tree %s (Grok envelope)", draft)
	}

	code := filepath.Join(repo, "internal", "cli", "cmd_hook.go")
	if gateEvent(t, repo, code) {
		t.Error("edit gate allowed in-repo product path with no engaged story")
	}
}

// TestHookGateTmpDraftAllowedWithCustomExemptList (sty_e33f78fe): a custom
// [gate] edit_exempt_paths that omits /tmp/ still allows a process-temp draft
// with no seat and with a live seat on a non-edit-capable plan step. In-repo
// product stays denied. The test repo is t.TempDir(), so the repo-under-temp
// containment case is exercised by construction.
func TestHookGateTmpDraftAllowedWithCustomExemptList(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	setExemptPaths(t, repo, ".satelle/")
	// setExemptPaths replaces satelle.toml; restore the hermetic create-gate
	// opt-out so story create does not fire create-review.
	writeFile(t, filepath.Join(repo, ".satelle", "satelle.local.toml"),
		"[review]\ngate_create = false\n\n[categories]\nenforce = \"off\"\n")

	draft := filepath.Join(os.TempDir(), "itest-temp-draft-gate", "scratch-notes.md")
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(draft) == repo || filepath.Dir(filepath.Dir(draft)) == repo {
		t.Fatalf("draft %q must not live inside repo %q", draft, repo)
	}
	code := filepath.Join(repo, "internal", "cli", "cmd_hook.go")
	grok := `{"toolInput":{"file_path":"` + draft + `"}}`

	// AC2 + AC3 (no engaged story): temp draft allows on both envelopes;
	// in-repo product is denied. Basename is not sty_* so no glob can pass it.
	if !gateEvent(t, repo, draft) {
		t.Errorf("no-seat: Claude envelope blocked process-temp %s", draft)
	}
	if !gateEventRaw(t, repo, grok) {
		t.Errorf("no-seat: Grok envelope blocked process-temp %s", draft)
	}
	if gateEvent(t, repo, code) {
		t.Error("no-seat: edit gate allowed in-repo product path")
	}

	// Gate-free route: plan is planner-allocated (not edit-capable) and
	// in-loop so the transition does not dispatch a planner CLI.
	writeSpineFixture(t, repo, "", "", "",
		"plan|planner|||",
		"in_progress|executor|||",
		"done||||")
	f, err := os.OpenFile(filepath.Join(repo, ".satelle", "workflows", "agents.toml"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[planner]\nrole = \"agent\"\ncommand = \"in-loop\"\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mustRun(t, testBin, repo, "reindex")

	out := mustRun(t, testBin, repo, "story", "create", "--category", "feature",
		"--title", "Draft a temp note", "--body", "planner writes a scratch file",
		"--acceptance", "1. temp draft allowed at plan")
	id := extractID(out, "sty_")
	if id == "" {
		t.Fatalf("no story id in:\n%s", out)
	}
	mustRun(t, testBin, repo, "story", "set", id, "--status", "plan")

	// AC1 + AC3 (live seat on plan): same allow / deny.
	if !gateEvent(t, repo, draft) {
		t.Errorf("plan seat: Claude envelope blocked process-temp %s", draft)
	}
	if !gateEventRaw(t, repo, grok) {
		t.Errorf("plan seat: Grok envelope blocked process-temp %s", draft)
	}
	if gateEvent(t, repo, code) {
		t.Error("plan seat: edit gate allowed in-repo product path")
	}
}
