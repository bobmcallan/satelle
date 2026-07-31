package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// reapFixture builds an isolated home containing:
//
//	linked   a plane whose repo root still exists     (must never be removed)
//	stale    a plane whose repo root has been deleted (the default target)
//	unknown  a plane with no marker and no registry entry
//
// and a workspace registry holding the live repo plus a deleted path that has
// NO plane at all — the observed case, where planes had been removed by hand and
// the registry entries survived.
type reapFixture struct {
	home        string
	liveRepo    string
	linkedDir   string
	staleDir    string
	unknownDir  string
	deletedRepo string // registry entry with no plane
}

func newReapFixture(t *testing.T) reapFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)

	plane := func(key, repo string) string {
		dir := filepath.Join(home, key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if repo != "" {
			if err := config.WriteRepoPathMarker(dir, repo); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	liveRepo := t.TempDir()

	goneRepo := t.TempDir()
	if err := os.RemoveAll(goneRepo); err != nil {
		t.Fatal(err)
	}
	deletedRepo := t.TempDir()
	if err := os.RemoveAll(deletedRepo); err != nil {
		t.Fatal(err)
	}

	f := reapFixture{
		home:        home,
		liveRepo:    liveRepo,
		linkedDir:   plane("live-11111111", liveRepo),
		staleDir:    plane("gone-22222222", goneRepo),
		unknownDir:  plane("leak-33333333", ""),
		deletedRepo: deletedRepo,
	}
	if err := config.SaveGlobal(config.GlobalConfig{
		Workspace: config.WorkspaceConfig{Repos: []string{liveRepo, deletedRepo}},
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f reapFixture) registry(t *testing.T) []string {
	t.Helper()
	gc, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	return gc.Workspace.Repos
}

// TestRuntimeReapDryRunReportsAndRemovesNothing (AC2): a bare invocation reports
// the plane path and the dangling repo path it resolved, then removes nothing —
// neither on disk nor in the registry.
func TestRuntimeReapDryRunReportsAndRemovesNothing(t *testing.T) {
	f := newReapFixture(t)
	before := f.registry(t)

	var out strings.Builder
	if err := runRuntimeReap(&out, false, false); err != nil {
		t.Fatal(err)
	}
	s := out.String()

	if !strings.Contains(s, f.staleDir) {
		t.Errorf("report must name the stale plane dir:\n%s", s)
	}
	if !strings.Contains(s, f.deletedRepo) {
		t.Errorf("report must name the dangling registry entry:\n%s", s)
	}
	if !strings.Contains(s, "--yes") {
		t.Errorf("report must name the explicit action required:\n%s", s)
	}

	for _, dir := range []string{f.staleDir, f.linkedDir, f.unknownDir} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("dry run must remove nothing, %s is gone: %v", dir, err)
		}
	}
	if got := f.registry(t); len(got) != len(before) {
		t.Errorf("dry run must not touch the registry: before=%v after=%v", before, got)
	}
}

// TestRuntimeReapRemovesStalePlanes (AC1): --yes removes the stale plane, and
// only what was reported. The unknown plane is out of scope by default.
func TestRuntimeReapRemovesStalePlanes(t *testing.T) {
	f := newReapFixture(t)

	var out strings.Builder
	if err := runRuntimeReap(&out, true, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(f.staleDir); !os.IsNotExist(err) {
		t.Errorf("stale plane should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(f.unknownDir); err != nil {
		t.Errorf("unknown plane is out of default scope and must survive: %v", err)
	}
	if _, err := os.Stat(f.linkedDir); err != nil {
		t.Errorf("linked plane must survive: %v", err)
	}
}

// TestRuntimeReapIncludeUnknownWidensToUnknownOnly (AC1/AC3): --include-unknown
// widens the target set to unknown planes and NOT to linked ones.
func TestRuntimeReapIncludeUnknownWidensToUnknownOnly(t *testing.T) {
	f := newReapFixture(t)

	var out strings.Builder
	if err := runRuntimeReap(&out, true, true); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(f.unknownDir); !os.IsNotExist(err) {
		t.Errorf("unknown plane should be removed with --include-unknown, stat err = %v", err)
	}
	if _, err := os.Stat(f.linkedDir); err != nil {
		t.Errorf("linked plane must survive --include-unknown: %v", err)
	}
}

// TestRuntimeReapNeverRemovesLinkedPlanes (AC3) is the safety invariant: across
// every flag combination, a plane whose repo root still exists survives, and so
// does its registry entry. The fixture is rebuilt per case so the matrix cases
// are independent.
func TestRuntimeReapNeverRemovesLinkedPlanes(t *testing.T) {
	for _, tc := range []struct{ yes, includeUnknown bool }{
		{false, false},
		{true, false},
		{false, true},
		{true, true},
	} {
		name := "yes=" + boolStr(tc.yes) + ",include-unknown=" + boolStr(tc.includeUnknown)
		t.Run(name, func(t *testing.T) {
			f := newReapFixture(t)

			var out strings.Builder
			if err := runRuntimeReap(&out, tc.yes, tc.includeUnknown); err != nil {
				t.Fatal(err)
			}

			if _, err := os.Stat(f.linkedDir); err != nil {
				t.Errorf("a plane whose repo exists must never be removed: %v", err)
			}
			if _, err := os.Stat(f.liveRepo); err != nil {
				t.Errorf("the live repo itself must never be touched: %v", err)
			}
			var found bool
			for _, p := range f.registry(t) {
				if p == f.liveRepo {
					found = true
				}
			}
			if !found {
				t.Errorf("the live repo's registry entry must survive, got %v", f.registry(t))
			}
			if strings.Contains(out.String(), f.linkedDir) {
				t.Errorf("a linked plane must not even be reported as a target:\n%s", out.String())
			}
		})
	}
}

// TestRuntimeReapClearsDanglingRegistryEntries (AC4): registry entries are
// cleared by the same action, including one whose plane never existed — and the
// live entry is left alone.
func TestRuntimeReapClearsDanglingRegistryEntries(t *testing.T) {
	f := newReapFixture(t)

	var out strings.Builder
	if err := runRuntimeReap(&out, true, false); err != nil {
		t.Fatal(err)
	}

	got := f.registry(t)
	if len(got) != 1 || got[0] != f.liveRepo {
		t.Fatalf("registry should hold exactly the live repo, got %v", got)
	}
}

// TestRuntimeReapCleanHomeSaysSo: the no-op case is an answer, not silence, and
// it mentions the unknown dirs it deliberately did not target.
func TestRuntimeReapCleanHomeSaysSo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SATELLE_HOME", home)
	live := t.TempDir()
	dir := filepath.Join(home, "live-44444444")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRepoPathMarker(dir, live); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveGlobal(config.GlobalConfig{
		Workspace: config.WorkspaceConfig{Repos: []string{live}},
	}); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runRuntimeReap(&out, false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "nothing to reap") {
		t.Errorf("a clean home should say so, got:\n%s", out.String())
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
