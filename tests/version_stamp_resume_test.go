//go:build integration

package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/structure"
)

// A story parked after its release commit and then resumed must still pass the
// version-stamp check against its ENGAGEMENT baseline (sty_1168e79f). Resuming
// re-anchors the diff (baseline_sha) at the release commit itself, so a check
// that read baseline_sha saw the bumped .version as "unchanged".
//
// The check is process substrate: its fence lives in the process-root skill
// .satelle/skills/satelle-version-stamp-check.md, git-ignored and so absent from
// CI. The test runs that real fence, not a copy.

const resumeStampWorkflowDone = `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "Fixture route: engage, park, resume, close."

["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
`

const resumeStampWorkflowStep = `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "Fixture step catalogue."

[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`

// processRootStampCheck returns the check fence of the process-root skill,
// resolving the process root as the check does: the dirname of the git common
// dir, so a linked worktree reaches the main tree's git-ignored substrate.
func processRootStampCheck(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "-C", moduleRoot(), "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatalf("resolve the process root from %s: %v", moduleRoot(), err)
	}
	skill := filepath.Join(filepath.Dir(strings.TrimSpace(string(out))), ".satelle", "skills", "satelle-version-stamp-check.md")
	body, err := os.ReadFile(skill)
	if os.IsNotExist(err) {
		t.Skip("process-root substrate satelle-version-stamp-check.md absent (git-ignored; not present in CI)")
	}
	if err != nil {
		t.Fatal(err)
	}
	fence := structure.CheckFence(string(body))
	if fence == "" {
		t.Fatalf("%s carries no ```check fence", skill)
	}
	return fence
}

func headSHA(t *testing.T, repo string) string {
	t.Helper()
	return strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
}

// resumedAfterRelease engages a story at commit A, optionally lands a release
// commit bumping .version, parks and resumes it, and returns the repo, the story
// id and A.
func resumedAfterRelease(t *testing.T, bump bool) (repo, sid, engagedAt string) {
	t.Helper()
	repo = t.TempDir()
	mustRun(t, testBin, repo, "init")
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.toml"), resumeStampWorkflowDone)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.toml"), resumeStampWorkflowStep)
	mustRun(t, testBin, repo, "reindex")

	gitInit(t, repo)
	mustWrite(t, filepath.Join(repo, ".version"), "satelle.version: 0.0.1\nsatelle.build: 2026-01-01-00-00-00\n")
	gitCommitAll(t, repo, "initial scaffold")

	sid = extractID(mustRun(t, testBin, repo, "story", "create", "--title", "Resume after release",
		"--category", "feature", "--body", "Parked after its release commit."), "sty_")
	if sid == "" {
		t.Fatal("no story id")
	}
	engagedAt = headSHA(t, repo)
	// The estimate gate guards the engaging edge; an empty comparables set means
	// no comparison, so any figure passes.
	mustRun(t, testBin, repo, "story", "estimate", sid, "--fresh-input", "1000", "--output", "1000")
	mustRun(t, testBin, repo, "story", "set", sid, "--status", "in_progress")

	if bump {
		mustWrite(t, filepath.Join(repo, ".version"), "satelle.version: 0.0.2\nsatelle.build: 2026-01-02-03-04-05\n")
		gitCommitAll(t, repo, "release: 0.0.2")
		gitIn(t, repo, "tag", "v0.0.2")
	} else {
		mustWrite(t, filepath.Join(repo, "notes.txt"), "no release here\n")
		gitCommitAll(t, repo, "work without a version bump")
	}

	mustRun(t, testBin, repo, "story", "set", sid, "--status", "blocked")
	mustRun(t, testBin, repo, "story", "set", sid, "--status", "in_progress")
	return repo, sid, engagedAt
}

// runStampFence runs the fence in repo with the built binary first on PATH.
func runStampFence(t *testing.T, fence, repo, sid string) (string, int) {
	t.Helper()
	binDir := t.TempDir()
	if err := os.Symlink(testBin, filepath.Join(binDir, "satelle")); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(t.TempDir(), "check.sh")
	if err := os.WriteFile(script, []byte(fence+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = repo
	cmd.Env = append(isolatedEnv(t), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdin = strings.NewReader(`{"story":{"id":"` + sid + `"},"from":"release","to":"done"}`)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	exit := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run fence: %v\n%s", err, buf.String())
		}
		exit = ee.ExitCode()
	}
	return buf.String(), exit
}

func TestVersionStampCheckAcceptsStoryResumedAfterItsRelease(t *testing.T) {
	fence := processRootStampCheck(t)
	repo, sid, engagedAt := resumedAfterRelease(t, true)

	out, exit := runStampFence(t, fence, repo, sid)
	if exit != 0 {
		t.Fatalf("exit=%d want 0 (release bumped 0.0.1 -> 0.0.2 after engagement)\n%s", exit, out)
	}
	if want := "engagement baseline " + engagedAt; !strings.Contains(out, want) {
		t.Errorf("output must name %q:\n%s", want, out)
	}
}

func TestVersionStampCheckRejectsStoryResumedWithoutABump(t *testing.T) {
	fence := processRootStampCheck(t)
	repo, sid, engagedAt := resumedAfterRelease(t, false)

	out, exit := runStampFence(t, fence, repo, sid)
	if exit != 1 {
		t.Fatalf("exit=%d want 1 (no version bump since engagement)\n%s", exit, out)
	}
	if want := "unchanged since engagement baseline " + engagedAt; !strings.Contains(out, want) {
		t.Errorf("output must contain %q:\n%s", want, out)
	}
}
