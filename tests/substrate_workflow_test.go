//go:build integration

package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubstrateWorkflowSeededAndDrivable proves the substrate LANE works as an
// authored route section (sty_825f45cc, re-homed by sty_3795e7f6 — the shipped
// default no longer declares one): a repo declares `## substrate`, the
// satelle-substrate-only-check gate skill resolves from the embedded set, a
// category:substrate story resolves to the lane rather than the wildcard one,
// the route validates, and it drives backlog -> in_progress -> done through the
// DETERMINISTIC close check — no LLM involved in that gate, so the test drives
// it for real rather than stubbing it.
func TestSubstrateWorkflowSeededAndDrivable(t *testing.T) {
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	// Virtual defaults: materialize the substrate workflow + gate for on-disk path checks.
	seedSubstrateLane(t, repo)
	materializeDefault(t, repo, "skills", "satelle-substrate-only-check")
	stubReviewerAccept(t, repo) // the step-summary node is an LLM reviewer; stub it
	mustRun(t, testBin, repo, "reindex")

	for _, rel := range []string{
		".satelle/workflows/done.toml",
		".satelle/workflows/step.toml",
		".satelle/skills/satelle-substrate-only-check.md",
	} {
		if _, err := os.Stat(filepath.Join(repo, rel)); err != nil {
			t.Fatalf("fixture did not land %s: %v", rel, err)
		}
	}

	if out, err := run(t, testBin, repo, "workflow", "validate", "done"); err != nil {
		t.Fatalf("validate failed: %v\n%s", err, out)
	} else if !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Errorf("validate did not pass cleanly:\n%s", out)
	}

	// category:substrate resolves to the authored route, which claims the section
	// and therefore outranks the shipped default.
	type wfRow struct {
		Name   string `json:"name"`
		Active bool   `json:"active"`
	}
	out := mustRun(t, testBin, repo, "workflow", "list", "--category", "substrate")
	var rows []wfRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse workflow list: %v\n%s", err, out)
	}
	if len(rows) == 0 || rows[0].Name != "default" || !rows[0].Active {
		t.Errorf("category substrate active lifecycle = %+v, want the authored route first/active", rows)
	}

	// Drive a real story through backlog -> in_progress -> done, with the close
	// gated by the deterministic substrate-only check against a real commit.
	// The scaffold (.gitignore, seeded .satelle/*) is committed FIRST, in a
	// commit that does not mention the story id, so the check's `git log
	// --grep=<sid>` only matches the later docs-only commit below.
	gitInit(t, repo)
	gitCommitAll(t, repo, "initial scaffold")
	sid := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Substrate test",
		"--category", "substrate", "--body", "Exercise the seeded substrate lane."), "sty_")
	if sid == "" {
		t.Fatalf("no story id")
	}
	mustRun(t, testBin, repo, "story", "set", sid, "--status", "in_progress")

	// Reject before any commit references the story.
	if out, err := run(t, testBin, repo, "story", "set", sid, "--status", "done"); err == nil {
		t.Errorf("close should be rejected with no commit referencing %s:\n%s", sid, out)
	}

	// Author + commit a substrate-only change (docs/) referencing the story id.
	docPath := filepath.Join(repo, "docs", "substrate-test.md")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte("# substrate test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repo, "docs: substrate test ("+sid+")")

	if out, err := run(t, testBin, repo, "story", "set", sid, "--status", "done"); err != nil {
		t.Fatalf("close should pass the substrate-only check once a commit references %s: %v\n%s", sid, err, out)
	}
}

// TestManagedFootprintClosesSubstrateLane proves an init-footprint commit
// (root .gitignore + init-deployed harness hook scaffold under .claude/ and/or
// .grok/) closes through the substrate lane without routing to the project/code
// workflow (sty_40973fb6). Modeled on TestSubstrateWorkflowSeededAndDrivable:
// init writes the managed footprint, a category:substrate story engages, a
// footprint edit is committed with the story id, and in_progress → done
// succeeds on the deterministic check.
func TestManagedFootprintClosesSubstrateLane(t *testing.T) {
	repo := t.TempDir()
	// Explicit harness so the managed footprint includes a hook settings file
	// (bare init no longer scaffolds harnesses by default).
	mustRun(t, testBin, repo, "init", "--harness", "claude")
	seedSubstrateLane(t, repo)
	materializeDefault(t, repo, "skills", "satelle-substrate-only-check")
	stubReviewerAccept(t, repo) // step-summary node is an LLM reviewer; stub it
	mustRun(t, testBin, repo, "reindex")

	// Commit the full init scaffold first (without a story id) so git log
	// --grep only matches the later managed-footprint edit.
	gitInit(t, repo)
	gitCommitAll(t, repo, "initial scaffold")

	sid := extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Land init footprint",
		"--category", "substrate", "--body", "Heal managed .gitignore and harness hooks."), "sty_")
	if sid == "" {
		t.Fatalf("no story id")
	}
	mustRun(t, testBin, repo, "story", "set", sid, "--status", "in_progress")

	// Managed-footprint edit: append to the root .gitignore init always writes,
	// plus the harness scaffold (--harness claude). All ride the substrate allow-list.
	giPath := filepath.Join(repo, ".gitignore")
	gi, err := os.ReadFile(giPath)
	if err != nil {
		t.Fatalf("init should have written root .gitignore: %v", err)
	}
	if err := os.WriteFile(giPath, append(gi, []byte("\n# footprint heal\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	harnessTouched := false
	for _, rel := range []string{
		".claude/settings.json",
		".grok/hooks/satelle.json",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		if err := os.WriteFile(p, append(b, []byte("\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		harnessTouched = true
	}
	if !harnessTouched {
		t.Fatal("init wrote no harness hook scaffold (.claude/settings.json or .grok/hooks/satelle.json)")
	}
	gitCommitAll(t, repo, "chore: heal init footprint ("+sid+")")

	if out, err := run(t, testBin, repo, "story", "set", sid, "--status", "done"); err != nil {
		t.Fatalf("init-footprint commit should close on the substrate lane for %s: %v\n%s", sid, err, out)
	}
}

func gitInit(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func gitCommitAll(t *testing.T, repo, message string) {
	t.Helper()
	add := exec.Command("git", "add", "-A")
	add.Dir = repo
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commit := exec.Command("git", "commit", "-q", "-m", message)
	commit.Dir = repo
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withTestBinOnPATH puts testBin first on PATH so functional-check scripts
// that shell out to bare `satelle` resolve the binary under test.
func withTestBinOnPATH(t *testing.T) []string {
	t.Helper()
	binDir := t.TempDir()
	link := filepath.Join(binDir, "satelle")
	if err := os.Symlink(testBin, link); err != nil {
		// Windows / restricted FS: copy instead.
		in, err := os.ReadFile(testBin)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(link, in, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := binDir + string(os.PathListSeparator) + os.Getenv("PATH")
	return []string{"PATH=" + path}
}

// TestSubstrateOnlyCheckFourPostures (sty_6469025e AC7): committed substrate
// and git-ignored uncommitted substrate accept; committed and uncommitted
// non-substrate reject. Empty commit alone rejects (AC3).
func TestSubstrateOnlyCheckFourPostures(t *testing.T) {
	pathEnv := withTestBinOnPATH(t)

	setup := func(t *testing.T) (repo, sid string) {
		t.Helper()
		repo = t.TempDir()
		mustRunEnv(t, testBin, repo, pathEnv, "init")
		seedSubstrateLane(t, repo)
		materializeDefault(t, repo, "skills", "satelle-substrate-only-check")
		stubReviewerAccept(t, repo)
		mustRunEnv(t, testBin, repo, pathEnv, "reindex")
		gitInit(t, repo)
		gitCommitAll(t, repo, "initial scaffold")
		sid = extractID(mustRunEnv(t, testBin, repo, pathEnv, "story", "create",
			"--title", "posture", "--category", "substrate",
			"--body", "four-posture coverage"), "sty_")
		if sid == "" {
			t.Fatal("no story id")
		}
		mustRunEnv(t, testBin, repo, pathEnv, "story", "set", sid, "--status", "in_progress")
		return repo, sid
	}

	t.Run("substrate committed accepts", func(t *testing.T) {
		repo, sid := setup(t)
		mustWrite(t, filepath.Join(repo, "docs", "ok.md"), "# ok\n")
		gitCommitAll(t, repo, "docs ("+sid+")")
		if _, err := runEnv(t, testBin, repo, pathEnv, "story", "set", sid, "--status", "done"); err != nil {
			t.Fatalf("want accept: %v", err)
		}
	})

	t.Run("substrate git-ignored uncommitted accepts", func(t *testing.T) {
		repo, sid := setup(t)
		// Ignore .satelle/ after scaffold so further edits are uncommitted-ignored.
		gi := filepath.Join(repo, ".gitignore")
		b, _ := os.ReadFile(gi)
		if err := os.WriteFile(gi, append(b, []byte("\n.satelle/\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		// Commit the gitignore change without the story id so it is not the slice.
		gitCommitAll(t, repo, "ignore satelle")
		// Author ignored skill + agents.toml after engage — live --include-substrate.
		mustWrite(t, filepath.Join(repo, ".satelle", "skills", "posture.md"), "# skill\n")
		mustWrite(t, filepath.Join(repo, ".satelle", "workflows", "agents.toml"), "[executor]\nrole = \"agent\"\n")
		if out, err := runEnv(t, testBin, repo, pathEnv, "story", "set", sid, "--status", "done"); err != nil {
			t.Fatalf("git-ignored substrate should close without commit: %v\n%s", err, out)
		}
	})

	t.Run("non-substrate committed rejects", func(t *testing.T) {
		repo, sid := setup(t)
		mustWrite(t, filepath.Join(repo, "pkg", "x.go"), "package pkg\n")
		gitCommitAll(t, repo, "code ("+sid+")")
		out, err := runEnv(t, testBin, repo, pathEnv, "story", "set", sid, "--status", "done")
		if err == nil {
			t.Fatalf("code commit should reject:\n%s", out)
		}
		if !strings.Contains(out, "x.go") && !strings.Contains(out, "pkg/") {
			t.Fatalf("reject must name the non-substrate path, got:\n%s", out)
		}
		if !strings.Contains(out, "non-substrate") {
			t.Fatalf("reject must cite non-substrate guardrail, got:\n%s", out)
		}
	})

	t.Run("non-substrate uncommitted rejects", func(t *testing.T) {
		repo, sid := setup(t)
		mustWrite(t, filepath.Join(repo, "pkg", "y.go"), "package pkg\n")
		// Also need some substrate signal or empty rejects first — add docs
		// so the union is non-empty but contains offenders.
		mustWrite(t, filepath.Join(repo, "docs", "note.md"), "# n\n")
		out, err := runEnv(t, testBin, repo, pathEnv, "story", "set", sid, "--status", "done")
		if err == nil {
			t.Fatalf("uncommitted code should reject:\n%s", out)
		}
		// AC2 path: offenders named. (Gate notes may embed the check script source,
		// which mentions the empty-set message as text — assert on the enacted notes.)
		if !strings.Contains(out, "touches non-substrate paths") {
			t.Fatalf("uncommitted non-substrate must reject as offender path, got:\n%s", out)
		}
		if !strings.Contains(out, "y.go") && !strings.Contains(out, "pkg/") {
			t.Fatalf("reject must name the uncommitted non-substrate path, got:\n%s", out)
		}
	})

	t.Run("empty commit alone rejects", func(t *testing.T) {
		repo, sid := setup(t)
		cmd := exec.Command("git", "commit", "--allow-empty", "-q", "-m", "empty ("+sid+")")
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("empty commit: %v\n%s", err, out)
		}
		out, err := runEnv(t, testBin, repo, pathEnv, "story", "set", sid, "--status", "done")
		if err == nil {
			t.Fatalf("empty commit must not close:\n%s", out)
		}
		if !strings.Contains(out, "no change set") {
			t.Fatalf("empty commit must reject with no-change-set message, got:\n%s", out)
		}
	})
}
