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
// documentation (sty_c7dfeedf): the file, the precedence ladder, and the
// no-implicit-merge guarantee.
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
	} {
		if !strings.Contains(body, want) {
			t.Errorf("`satelle help global-agents` missing %q:\n%s", want, body)
		}
	}
}
