package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_12499ecc AC4, end to end: the REAL engine as the transition gater, a step
// that declares a context source, a story carrying a requirement reference, and a
// fake reviewer that reports one lookup through the offered source and one
// through a source nobody offered. The verdict's ledger row records the offered
// source and only the offered lookup, and the route shows both. "src" and
// ref:jira:ID-12 are fixture data.

const ctxReviewSkill = `---
name: ctx-plan-review
type: skill
scope: project
tags: [type:skill, type:reviewer]
description: Fixture gate on plan → in_progress for the context-sources test.
---

# Fixture plan review

Judge the plan against the story.

Return JSON {"decision": "accept"|"reject", "notes": "…"}.
`

var ctxRouteWorkflow = routeHalves(
	`[feature]
obligations = ["raised", "planned", "coded", "closed"]
`,
	`[raised]
status = "backlog"
start = true

[planned]
status = "plan"
requires = ["raised"]

[coded]
status = "in_progress"
reviewers = ["ctx-plan-review"]
reviewer_agent = "reviewer"
context = ["src"]
requires = ["planned"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)

func TestVerdictLedgerRowAndRouteRecordOfferedAndConsulted(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetStoryDir(filepath.Join(t.TempDir(), "stories"))
	cfg := config.Config{Tags: config.TagsConfig{References: []string{"ref"}}}
	verb.SetTagVocabulary(cfg)

	wfDir, skillDir := t.TempDir(), t.TempDir()
	writeRouteFiles(t, wfDir, ctxRouteWorkflow)
	if err := os.WriteFile(filepath.Join(skillDir, "ctx-plan-review.md"), []byte(ctxReviewSkill), 0o644); err != nil {
		t.Fatal(err)
	}
	call(t, "doc-sync", map[string]any{"dirs": map[string]string{"workflows": wfDir, "skills": skillDir}})

	runner := &captureRunner{outs: []string{
		`{"decision":"accept","notes":"checked the requirement","consulted":[{"source":"src","ref":"jira:ID-12"},{"source":"nope","ref":"x"}]}`,
	}}
	rev := agentstep.New(runner, db.DocIndex, t.TempDir(), "")
	rev.SetContextSources(func(name string) (agentcli.ContextServer, bool) {
		if name == "src" {
			return agentcli.ContextServer{Name: "src", Type: agentcli.ContextHTTP, URL: "https://ctx.example.test/mcp", Tools: []string{"get_issue"}}, true
		}
		return agentcli.ContextServer{}, false
	})
	rev.SetReferences(cfg.References)
	verb.SetTransitionGater(rev)

	var story workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{
		"title": "context rides the verdict", "category": "feature", "body": "b",
		"acceptance_criteria": "1. a", "tags": []string{"ref:jira:ID-12"},
	}), &story); err != nil {
		t.Fatal(err)
	}
	call(t, "story-set", map[string]any{"id": story.ID, "status": "plan"})
	call(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"})

	if len(runner.reqs) != 1 {
		t.Fatalf("reviewer ran %d times, want 1", len(runner.reqs))
	}
	q := runner.reqs[0]
	if len(q.ContextServers) != 1 || q.ContextServers[0].Name != "src" {
		t.Errorf("the reviewer was not offered the declared source: %+v", q.ContextServers)
	}
	if !strings.Contains(q.SystemPrompt, "## Context sources") || !strings.Contains(q.SystemPrompt, "ref:jira:ID-12") {
		t.Errorf("the briefing does not list the source and the story's reference:\n%s", q.SystemPrompt)
	}

	rows := reviewPayloads(t, story.ID, "review_accept")
	var row map[string]any
	for _, r := range rows {
		if r["to"] == "in_progress" {
			row = r
		}
	}
	if row == nil {
		t.Fatalf("no review_accept row for plan → in_progress: %+v", rows)
	}
	if got, _ := json.Marshal(row["context_sources"]); string(got) != `["src"]` {
		t.Errorf("context_sources = %s, want [src]", got)
	}
	consulted, _ := row["consulted"].([]any)
	if len(consulted) != 1 {
		t.Fatalf("consulted = %v, want only the offered lookup", row["consulted"])
	}
	if c, _ := consulted[0].(map[string]any); c["source"] != "src" || c["ref"] != "jira:ID-12" {
		t.Errorf("consulted[0] = %v", c)
	}
	if row["consulted_dropped"] != float64(1) {
		t.Errorf("consulted_dropped = %v, want 1", row["consulted_dropped"])
	}

	route, err := verb.StoryRoute(context.Background(), story.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  - context: src\n", "  - consulted: src jira:ID-12\n", "## References", "jira ID-12"} {
		if !strings.Contains(route, want) {
			t.Errorf("route missing %q:\n%s", want, route)
		}
	}
	if strings.Contains(route, "nope") {
		t.Errorf("route names an unoffered source:\n%s", route)
	}
}
