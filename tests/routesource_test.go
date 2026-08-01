//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/wfdot"
)

// This repo's lifecycle is a DERIVED route: `.satelle/workflows/done.md` +
// `step.md`, with the four DOT graphs retired (sty_9835070d). These helpers
// replace the "read satelle-<x>-workflow.md" idiom the black-box tests used to
// seed and inspect it, so a test asserts against the lifecycle the repo actually
// runs rather than a file that no longer exists.

// repoRouteSource returns this repo's declaration of done and step catalogue.
func repoRouteSource(t *testing.T) (done, step string) {
	t.Helper()
	dir := filepath.Join(repoRootForTest(), ".satelle", "workflows")
	d, err := os.ReadFile(filepath.Join(dir, "done.md"))
	if err != nil {
		t.Fatalf("read done.md: %v", err)
	}
	s, err := os.ReadFile(filepath.Join(dir, "step.md"))
	if err != nil {
		t.Fatalf("read step.md: %v", err)
	}
	return string(d), string(s)
}

// seedRouteSource copies this repo's route source into a temp repo's substrate,
// so the seeded repo is governed by the same lifecycle this one is.
func seedRouteSource(t *testing.T, repo string) {
	t.Helper()
	done, step := repoRouteSource(t)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"), step)
}

// seedRouteSourceWith copies the route source after applying a transform to each
// half — for tests that need a deliberately altered lifecycle (a missing gate, a
// renamed skill) without hand-authoring a second one.
func seedRouteSourceWith(t *testing.T, repo string, transform func(done, step string) (string, string)) {
	t.Helper()
	done, step := repoRouteSource(t)
	done, step = transform(done, step)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"), done)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"), step)
}

// repoRouteSpec builds this repo's route for a category — the Spec a story of
// that category is actually governed by.
func repoRouteSpec(t *testing.T, category string, tags []string) wfdot.Spec {
	t.Helper()
	done, step := repoRouteSource(t)
	spec, err := wfdot.ParseRoute(done, step, category, tags)
	if err != nil {
		t.Fatalf("derive route for category %q: %v", category, err)
	}
	return spec
}
