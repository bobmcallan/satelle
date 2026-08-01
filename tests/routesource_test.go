//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
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

// substrateLaneDone / substrateLaneStep are a MINIMAL authored route carrying a
// substrate lane. The shipped default route deliberately has no `## substrate`
// section (sty_3795e7f6): the lane exists to let a markdown-only change skip a
// heavier lane, and the default has exactly one working lane to skip. A repo
// that wants the lane declares it — which is what these fixtures are, and why
// the substrate-only check keeps being exercised for real here.
const substrateLaneDone = `---
name: done
type: workflow
scope: project
description: Fixture declaration of done carrying a substrate lane beside the default one.
---

## *
- raised
- coded
- closed
cancel: cancelled @satelle-story-cancel-review

## substrate
- raised
- authored
- substrate-verified
cancel: cancelled @satelle-story-cancel-review
`

const substrateLaneStep = `---
name: step
type: workflow
scope: project
description: Fixture step catalogue for the substrate-lane route.
---

## backlog
start: true
provides: raised

## in_progress
agent: executor
reviewers: satelle-story-intent-review
reviewer_agent: reviewer
provides: coded
requires: raised

## done
reviewers: satelle-story-done-review
reviewer_agent: reviewer
terminal: true
provides: closed
requires: coded

## in_progress
agent: executor
provides: authored
requires: raised

## done
terminal: true
provides: substrate-verified
requires: authored

## gate satelle-step-summary
agent: reviewer
mandatory: true
for: *, substrate

## gate satelle-substrate-only-check
on: done
for: substrate
`

// seedSubstrateLane installs the fixture route above, so a temp repo has a
// category:substrate lane closed by the deterministic substrate-only check.
func seedSubstrateLane(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "done.md"), substrateLaneDone)
	writeFile(t, filepath.Join(repo, ".satelle", "workflows", "step.md"), substrateLaneStep)
}

// embeddedRouteHalves returns the route source the BINARY ships — the order-zero
// lifecycle a repo with no substrate of its own is governed by (sty_3795e7f6).
func embeddedRouteHalves(t *testing.T) (done, step string) {
	t.Helper()
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind != "workflows" {
			continue
		}
		switch d.Name {
		case "done":
			done = d.Body
		case "step":
			step = d.Body
		}
	}
	if done == "" || step == "" {
		t.Fatal("the binary must ship both halves of the default route")
	}
	return done, step
}

// embeddedRouteCategories lists the categories the shipped route declares.
func embeddedRouteCategories(t *testing.T) []string {
	t.Helper()
	done, _ := embeddedRouteHalves(t)
	lists, err := wfdot.ParseDone(done)
	if err != nil {
		t.Fatalf("parse the shipped done.md: %v", err)
	}
	out := make([]string, 0, len(lists))
	for _, l := range lists {
		out = append(out, l.Category)
	}
	return out
}

// embeddedRouteSpec builds the SHIPPED route for a category.
func embeddedRouteSpec(t *testing.T, category string, tags []string) wfdot.Spec {
	t.Helper()
	done, step := embeddedRouteHalves(t)
	spec, err := wfdot.ParseRoute(done, step, category, tags)
	if err != nil {
		t.Fatalf("derive the shipped route for %q: %v", category, err)
	}
	return spec
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
