package agentstep

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// TestCharterSourcesRenderBothRoles: the reviewer and executor charters come from
// ONE source (invoke.go) — each renders its own identity + constraint, and both
// carry the shared briefing (the .satelle read instruction), proving there is no
// per-role copied prose (AC2).
func TestCharterSourcesRenderBothRoles(t *testing.T) {
	rc := reviewerCharter()
	for _, want := range []string{"reviewer", "read-only", "return your verdict"} {
		if !strings.Contains(rc, want) {
			t.Errorf("reviewer charter missing %q: %q", want, rc)
		}
	}
	ec := executorCharter("planner", "plan", "satelle-project-workflow")
	for _, want := range []string{"planner", "plan", "satelle-project-workflow", "Do NOT change the item's status"} {
		if !strings.Contains(ec, want) {
			t.Errorf("executor charter missing %q: %q", want, ec)
		}
	}
	// The shared briefing rides in BOTH — one source, not two copies.
	if !strings.Contains(rc, ".satelle/") || !strings.Contains(ec, ".satelle/") {
		t.Errorf("shared briefing (.satelle read) missing from a role charter:\nreviewer=%q\nexecutor=%q", rc, ec)
	}
}

// TestBuildRequestCanonicalOrderAndOmitsEmpty: buildRequest composes the prompt in
// ONE canonical order — principles, then charter, then rubric — separated by the
// horizontal rule, and OMITS empty sections so a rubric-only invocation (the
// summariser) yields the rubric verbatim (AC1).
func TestBuildRequestCanonicalOrderAndOmitsEmpty(t *testing.T) {
	docs := fakeDocs{
		workflow: testWorkflow,
		extraPrinciples: []docindex.Doc{
			{Kind: "principles", Name: config.OperatingPrinciple, Body: alwaysPrincipleDoc},
		},
	}
	g := New(&fakeRunner{}, docs, "/repo", "")

	req, err := g.buildRequest(context.Background(), invocation{
		charter:          reviewerCharter(),
		rubric:           "RUBRIC-BODY",
		injectPrinciples: true,
		payload:          map[string]string{"k": "v"},
		tools:            "Read,Grep",
		model:            "sonnet",
	})
	if err != nil {
		t.Fatal(err)
	}
	sp := req.SystemPrompt
	iPrin := strings.Index(sp, "This resident belief MUST be visible")
	iChar := strings.Index(sp, "isolated satelle reviewer")
	iRub := strings.Index(sp, "RUBRIC-BODY")
	if iPrin < 0 || iChar < 0 || iRub < 0 || !(iPrin < iChar && iChar < iRub) {
		t.Errorf("canonical order principles<charter<rubric violated (%d,%d,%d):\n%s", iPrin, iChar, iRub, sp)
	}
	if !strings.Contains(sp, "\n\n---\n\n") {
		t.Errorf("charter/rubric separator missing:\n%s", sp)
	}
	if req.AllowedTools != "Read,Grep" || req.Model != "sonnet" || req.Dir != "/repo" {
		t.Errorf("grant/model/dir not carried: %+v", req)
	}
	if req.Payload != `{"k":"v"}` {
		t.Errorf("payload not marshalled onto stdin: %q", req.Payload)
	}

	// Summariser shape: no charter + no principle injection → the prompt is the
	// rubric verbatim (empty sections omitted).
	req2, err := g.buildRequest(context.Background(), invocation{rubric: "JUST-RUBRIC"})
	if err != nil {
		t.Fatal(err)
	}
	if req2.SystemPrompt != "JUST-RUBRIC" {
		t.Errorf("an empty charter/principles must yield a rubric-only prompt, got %q", req2.SystemPrompt)
	}
}

// TestRunOnceUsesSuppliedRunnerAndTimeout: runOnce runs the runner it is GIVEN
// (the executor's binding runner, not the engine's g.runner) and honours the
// per-invocation deadline — the two ways executor dispatch differs from a gate run.
func TestRunOnceUsesSuppliedRunnerAndTimeout(t *testing.T) {
	g := New(&fakeRunner{out: "ENGINE"}, fakeDocs{workflow: testWorkflow}, "/repo", "")

	supplied := &fakeRunner{out: "SUPPLIED"}
	out, err := g.runOnce(context.Background(), supplied, agentcli.Request{SystemPrompt: "x"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "SUPPLIED" {
		t.Errorf("runOnce must use the SUPPLIED runner, not g.runner, got %q", out)
	}
	if supplied.got.SystemPrompt != "x" {
		t.Errorf("request not passed through to the supplied runner: %+v", supplied.got)
	}

	if _, err := g.runOnce(context.Background(), &blockingRunner{}, agentcli.Request{}, time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("runOnce must honour the per-invocation timeout, got %v", err)
	}
}
