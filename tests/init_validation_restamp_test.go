//go:build integration

package tests

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/health"
)

// stampRefused reports whether out carries the drift gate's stamp refusal.
func stampRefused(out string) bool {
	return strings.Contains(out, health.StampRefusalMarker) || strings.Contains(out, "across BREAKING")
}

// TestInitRestampsDespiteUnrelatedValidationFailure (sty_7ef14c27 AC1, AC2): a
// repo whose agents.local.toml grants the * lane's seats Claude-named tools with
// no context channel (a node.alloc problem no migration touches, and not one
// init's foreign-tool repair rewrites, sty_f7f80c48) sits behind an init-heals
// release.
// init applies the migrations, re-stamps and still fails on the node.alloc
// finding — and the stamp stays current, so the store-backed commands run and the
// substrate story that fixes the problem engages, while the * lane is refused for
// node.alloc alone. Proven for a single init and for init --all --yes.
func TestInitRestampsDespiteUnrelatedValidationFailure(t *testing.T) {
	if !isReleaseTestBin(t) {
		t.Skip("testBin is not release-stamped (dev sentinel) — a dev binary never stamps or gates")
	}
	for _, mode := range []string{"init", "init --all --yes"} {
		t.Run(mode, func(t *testing.T) {
			// Each mode gets its own home, so init --all sees only this repo.
			env := []string{"SATELLE_HOME=" + t.TempDir()}
			repo := t.TempDir()
			sat := func(args ...string) (string, error) { return runEnv(t, testBin, repo, env, args...) }

			mustRunEnv(t, testBin, repo, env, "init")
			binVer := readStamp(t, repo)
			brokenWildcardLane(t, repo)
			writeFile(t, filepath.Join(repo, ".satelle", "workflows", "agents.local.toml"),
				"[planner]\nrole = \"agent\"\ntools = \"Grep,Glob,LS\"\n\n[coder]\nrole = \"agent\"\ntools = \"Edit,Write\"\n")
			mustRunEnv(t, testBin, repo, env, "substrate", "edit", "skills", "satelle-substrate-only-check")
			stubReviewerAccept(t, repo)
			mustRunEnv(t, testBin, repo, env, "reindex")
			gitInit(t, repo)
			gitCommitAll(t, repo, "initial scaffold")

			// Stories are created while the stamp is current: story create is
			// store-backed and is refused once the stamp is lowered.
			sid := extractID(mustRunEnv(t, testBin, repo, env, "story", "create", "--title", "Restate local tools",
				"--category", "substrate", "--body", "Restate agents.local.toml tools in Claude's names."), "sty_")
			bug := extractID(mustRunEnv(t, testBin, repo, env, "story", "create", "--title", "Fix a widget",
				"--category", "bug", "--body", "The widget does not render.", "--acceptance", "1. the widget renders"), "sty_")

			writeStamp(t, repo, "0.0.1")
			if out, err := sat("status"); err == nil || !strings.Contains(out, health.StampRefusalMarker) {
				t.Fatalf("guard: a stamp of 0.0.1 must refuse status across an init-heals release: err=%v\n%s", err, out)
			}

			// AC1: init applies the migrations, re-stamps, and fails on the
			// unrelated validation problem, reported as its own finding.
			var out string
			var err error
			if mode == "init" {
				out, err = sat("init")
				if err == nil || !strings.Contains(out, "the deployed system failed validation") ||
					!strings.Contains(out, "FAIL  ["+health.IDNodeAlloc+"]") {
					t.Fatalf("init must exit non-zero and print the node.alloc FAIL finding: err=%v\n%s", err, out)
				}
			} else {
				out, err = sat("init", "--all", "--yes")
				if err == nil || !strings.Contains(out, "FAILED  "+repo) ||
					!strings.Contains(out, "the deployed system failed validation") || !strings.Contains(out, "0 healed, 1 failed") {
					t.Fatalf("init --all --yes must exit non-zero and count the repo failed on its validation: err=%v\n%s", err, out)
				}
			}
			if got := readStamp(t, repo); got != binVer {
				t.Fatalf("a validation failure must leave the stamp current: stamp = %s, want %s\n%s", got, binVer, out)
			}

			// AC2: the store-backed commands run without the stamp refusal.
			for _, args := range [][]string{{"status"}, {"story", "get", sid}} {
				if o, e := sat(args...); e != nil || stampRefused(o) {
					t.Errorf("%s must run without the stamp refusal after init: err=%v\n%s", strings.Join(args, " "), e, o)
				}
			}
			// The substrate fix story engages on its lane's declared edge.
			if o, e := sat("story", "set", sid, "--status", "in_progress"); e != nil {
				t.Errorf("the substrate fix story must engage backlog → in_progress: %v\n%s", e, o)
			}
			// A * lane story is refused for node.alloc, never for the stamp.
			if o, e := sat("story", "set", bug, "--status", "plan"); e == nil ||
				!strings.Contains(o, health.IDNodeAlloc) || stampRefused(o) {
				t.Errorf("a * lane story must be refused with node.alloc and not the stamp refusal: err=%v\n%s", e, o)
			}
			// Doctor still reports the problem the migrations do not touch.
			if o, e := sat("doctor"); e == nil || !strings.Contains(o, "["+health.IDNodeAlloc+"]") {
				t.Errorf("doctor must still report [%s]: err=%v\n%s", health.IDNodeAlloc, e, o)
			}
		})
	}
}

// TestInitMigrationFailureKeepsStamp (sty_7ef14c27 AC3): when a required init
// migration fails — the retired [sync] hold_stale_after key cannot be removed from
// a read-only satelle.toml — init exits non-zero before the stamp line, so
// deployed.version keeps its bytes and status still refuses across the init-heals
// release.
func TestInitMigrationFailureKeepsStamp(t *testing.T) {
	if !isReleaseTestBin(t) {
		t.Skip("testBin is not release-stamped (dev sentinel) — a dev binary never stamps or gates")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes, so the read-only satelle.toml would be written")
	}
	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	tomlPath := filepath.Join(repo, ".satelle", "satelle.toml")
	raw, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	seeded := strings.Replace(string(raw), "[sync]\n", "[sync]\nhold_stale_after = \"2h\"\n", 1)
	if seeded == string(raw) {
		t.Fatal("fixture: scaffold [sync] table not found")
	}
	if err := os.WriteFile(tomlPath, []byte(seeded), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tomlPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(tomlPath, 0o644) })
	writeStamp(t, repo, "0.0.1")
	stampPath := filepath.Join(repo, ".satelle", "deployed.version")
	before, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatal(err)
	}

	if out, err := run(t, testBin, repo, "init"); err == nil {
		t.Fatalf("init must exit non-zero when the retired [sync] key cannot be removed:\n%s", out)
	}
	if after, err := os.ReadFile(stampPath); err != nil || !bytes.Equal(after, before) {
		t.Errorf("a failed migration must leave deployed.version as it was: got %q %v, want %q", after, err, before)
	}
	if out, err := run(t, testBin, repo, "status"); err == nil || !strings.Contains(out, health.StampRefusalMarker) ||
		strings.Contains(out, "bootstrap:") {
		t.Errorf("status must still refuse with the stamp marker after a failed migration: err=%v\n%s", err, out)
	}
}
