//go:build integration

package tests

import (
	"strings"
	"testing"
)

// TestHelpProjectsTopic drives the real binary: the new projects topic is listed
// by `satelle help` and `satelle help projects` teaches the workspace-add path.
func TestHelpProjectsTopic(t *testing.T) {
	dir := t.TempDir()
	if list := mustRun(t, testBin, dir, "help"); !strings.Contains(list, "projects") {
		t.Errorf("`satelle help` does not list the projects topic:\n%s", list)
	}
	body := mustRun(t, testBin, dir, "help", "projects")
	for _, want := range []string{"landing", "workspace add", "/<slug>/", "service install"} {
		if !strings.Contains(body, want) {
			t.Errorf("`satelle help projects` missing %q:\n%s", want, body)
		}
	}
	// The topic must teach the NEW model, not the retired "bound repo never moves
	// from /" one.
	if strings.Contains(body, "never moves") {
		t.Errorf("`satelle help projects` still describes the retired bound-repo-at-/ model:\n%s", body)
	}
}

// TestHelpLifecycleHookTopics pins AC7: the two topics teach the hook grammar
// and the four-way distinction the story names, through the real binary.
func TestHelpLifecycleHookTopics(t *testing.T) {
	dir := t.TempDir()

	createReview := mustRun(t, testBin, dir, "help", "create-review")
	for _, want := range []string{
		"lifecycle hook",
		"hooks:",
		"operation: create_review",
		"agent: strict-reviewer",
		"Shorthand",
		"satelle workflow show",
		"never **how**",
	} {
		if !strings.Contains(createReview, want) {
			t.Errorf("`satelle help create-review` missing %q:\n%s", want, createReview)
		}
	}

	workflows := mustRun(t, testBin, dir, "help", "workflows")
	for _, want := range []string{
		"Step gates",
		"Lifecycle hooks",
		"Deterministic structure checks",
		"Agent judgments",
		"outside the status graph",
	} {
		if !strings.Contains(workflows, want) {
			t.Errorf("`satelle help workflows` missing %q:\n%s", want, workflows)
		}
	}
}

// TestHelpGlobalAgentsTopic pins the machine-wide profile catalog's product
// documentation (sty_c7dfeedf / sty_552d2d87 / sty_a319db89): the file, the
// precedence ladder, the no-implicit-merge guarantee, and the self-sufficient
// committed-substrate rationale (no dangling external decision record).
func TestHelpGlobalAgentsTopic(t *testing.T) {
	dir := t.TempDir()
	if list := mustRun(t, testBin, dir, "help"); !strings.Contains(list, "global-agents") {
		t.Errorf("`satelle help` does not list the global-agents topic:\n%s", list)
	}
	body := mustRun(t, testBin, dir, "help", "global-agents")
	for _, want := range []string{
		"~/.satelle/agents.toml",
		"profile = ",
		"no implicit same-name merge",
		"satelle agent profiles",
		"satelle agent migrate",
		"embedded",
		// sty_552d2d87 — committed substrate posture + fresh-clone path
		".satelle/workflows/agents.toml",
		"committed substrate",
		"satelle agent validate",
		// sty_940938e3 — personal catalog backup
		"agent profiles push",
		"[vars]",
		// sty_a319db89 — rationale is inline; help is self-sufficient
		"Why committed",
		"fail-closed",
		"agent=",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("`satelle help global-agents` missing %q:\n%s", want, body)
		}
	}
	// Dangling dogfood-repo pointer must not return (sty_a319db89 AC2/AC4).
	if strings.Contains(strings.ToLower(body), "dogfood") {
		t.Error("`satelle help global-agents` must not reference a dogfood repo or dogfood decision record")
	}
}
