package agentstep

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

type textRunner struct {
	mu    sync.Mutex
	reqs  []agentcli.Request
	reply string
}

func (r *textRunner) Name() string    { return "text" }
func (r *textRunner) Command() string { return "text" }
func (r *textRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return []byte(r.reply), nil
}

func reviewTextEngine(r agentcli.Runner) *Engine {
	g := New(nil, nil, "", "")
	g.SetWarnWriter(io.Discard)
	g.SetRunnerFactory(func(string, string) (agentcli.Runner, error) { return r, nil })
	return g
}

const reviewerCmd = "claude -p --output-format json --append-system-prompt {system} --allowedTools {tools}"

// ReviewText is the reviewer run path without a work item: read-only, the
// rubric as the system prompt, the payload as JSON, free text back.
func TestReviewTextRunsAReadOnlyReviewerAndReturnsItsText(t *testing.T) {
	r := &textRunner{reply: "  the reply  "}
	res, err := reviewTextEngine(r).ReviewText(context.Background(), ReviewTextRequest{
		Section: config.RoleReviewer,
		Binding: config.AgentBinding{Role: config.RoleReviewer, Command: reviewerCmd, Tools: "Read,Grep,Glob"},
		Skill:   "satelle-context-audit", Rubric: "RUBRIC-BODY", Payload: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "the reply" || res.Adapter != agentcli.HarnessClaude || res.PayloadBytes == 0 {
		t.Fatalf("got %+v", res)
	}
	if len(r.reqs) != 1 {
		t.Fatalf("one-shot: want one run, got %d", len(r.reqs))
	}
	req := r.reqs[0]
	if !req.ReadOnly || req.AllowedTools != "Read,Grep,Glob" {
		t.Errorf("must be read-only under the binding's grant: %+v", req)
	}
	if !strings.Contains(req.SystemPrompt, "RUBRIC-BODY") || req.Payload != `{"k":"v"}` {
		t.Errorf("rubric/payload not delivered: prompt=%.120q payload=%q", req.SystemPrompt, req.Payload)
	}
}

func TestReviewTextRefusesAnInLoopOrNonReviewerBinding(t *testing.T) {
	g := reviewTextEngine(&textRunner{reply: "x"})
	_, err := g.ReviewText(context.Background(), ReviewTextRequest{
		Section: "reviewer", Binding: config.AgentBinding{Role: config.RoleReviewer, Command: "in-loop"},
	})
	if err == nil || !strings.Contains(err.Error(), "in-loop") {
		t.Fatalf("an in-loop binding dispatches nothing: %v", err)
	}
	_, err = g.ReviewText(context.Background(), ReviewTextRequest{
		Section: "planner", Binding: config.AgentBinding{Role: "agent", Command: reviewerCmd},
	})
	if err == nil || !strings.Contains(err.Error(), "want role=reviewer") {
		t.Fatalf("a performer binding is not a reviewer: %v", err)
	}
}

func TestReviewTextEmptyOutputIsAnError(t *testing.T) {
	_, err := reviewTextEngine(&textRunner{reply: "   "}).ReviewText(context.Background(), ReviewTextRequest{
		Section: "reviewer", Binding: config.AgentBinding{Role: config.RoleReviewer, Command: reviewerCmd},
	})
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("got %v", err)
	}
}
