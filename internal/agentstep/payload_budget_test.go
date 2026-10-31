package agentstep

// sty_73a2dc68 AC3: every isolated dispatch measures what it sends — the system
// prompt plus the stdin payload — against its seat's [validate.injected.budget]
// row, and refuses an over-budget dispatch with an error naming the seat, the
// step and both sizes. Nothing is truncated: the runner never sees a request.
// The budgets here are fixture numbers.

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/condense"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// budgetFor is an injected table giving seat a budget of tokens, one byte each.
func budgetFor(seat string, tokens int) config.InjectedConfig {
	return config.InjectedConfig{BytesPerToken: 1, Budget: map[string]int{seat: tokens}}
}

// assertRefused checks a budget refusal names the seat, the step, the budget
// and the size actually sent, and points at the principle rather than at a
// smaller batch.
func assertRefused(t *testing.T, err error, seat, step string, budget int) {
	t.Helper()
	if err == nil {
		t.Fatalf("an over-budget dispatch for [%s] must be refused", seat)
	}
	msg := err.Error()
	for _, want := range []string{
		"over budget", strconv.Quote(seat), strconv.Quote(step),
		"budget " + strconv.Itoa(budget) + " bytes", "satelle-dispatch-pointers",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not name %q", msg, want)
		}
	}
	sent := msg[strings.Index(msg, "sends ")+len("sends "):]
	n, convErr := strconv.Atoi(sent[:strings.Index(sent, " ")])
	if convErr != nil || n <= budget {
		t.Errorf("refusal must name the size sent (> %d), got %q", budget, msg)
	}
}

// oneShotDispatch runs DispatchExecutor for the coder seat under inj with a
// story body of bodyBytes, returning the runner and the error.
func oneShotDispatch(t *testing.T, inj config.InjectedConfig, bodyBytes int) (*fakeRunner, error) {
	t.Helper()
	r := &fakeRunner{out: budgetEnvelope(t, 1)}
	return r, coderDispatch(t, inj, r, strings.Repeat("b", bodyBytes), nil)
}

// coderDispatch runs DispatchExecutor for the coder seat under inj on runner,
// with the given story body and attached documents.
func coderDispatch(t *testing.T, inj config.InjectedConfig, runner agentcli.Runner, body string, docs []DocState) error {
	t.Helper()
	wf := spineWF("", "", "", "in_progress|coder|planning-rubric", "done")
	const rubric = "---\nname: planning-rubric\ntype: skill\ndescription: test rubric\n---\nDo the work."
	g, _ := newEngine(t, "", fakeDocs{workflow: wf, skillBody: rubric, skillFound: true})
	g.newRunner = func(string, string) (agentcli.Runner, error) { return runner, nil }
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Tools: "read_file,grep,list_dir", Command: "fake -p {system}"}, true
	})
	g.SetDocsResolver(func(context.Context, string) []DocState { return docs })
	g.SetInjected(inj, condense.Rules{})
	item := workitem.Item{ID: "sty_budget", Status: "backlog", Body: body}
	_, err := g.DispatchExecutor(context.Background(), item, "in_progress")
	return err
}

// pathlessRunner is a performer that cannot read this machine's scratch paths.
type pathlessRunner struct{ *fakeRunner }

func (pathlessRunner) localPathsUnreadable() {}

// TestPerformerDocumentsGoByPath (sty_73a2dc68, satelle-dispatch-pointers): a
// local performer can read its own documents, so it is sent each one as a path
// to a scratch copy rather than the body — a plan larger than the seat's whole
// budget then dispatches. A performer that cannot read this machine keeps the
// bodies inline, and is measured with them.
func TestPerformerDocumentsGoByPath(t *testing.T) {
	plan := DocState{Name: "plan", Type: "plan", Body: "PLAN-SENTINEL " + strings.Repeat("p", 30000)}
	docs := []DocState{plan}
	inj := budgetFor("coder", 15000)

	local := &fakeRunner{out: budgetEnvelope(t, 1)}
	if err := coderDispatch(t, inj, local, "story body", docs); err != nil {
		t.Fatalf("a local performer's documents go by path, so a large plan must not refuse it: %v", err)
	}
	if strings.Contains(local.got.Payload, "PLAN-SENTINEL") {
		t.Errorf("a local performer must be sent the plan's path, not its body:\n%s", local.got.Payload)
	}
	scratch := local.got.Env[config.ScratchEnv]
	var read bool
	for p, b := range local.opened {
		read = read || (string(b) == plan.Body && scratch != "" && strings.HasPrefix(p, scratch))
	}
	if !read {
		t.Errorf("the payload's doc path must hold the full plan under the dispatch scratch %q; opened %d file(s)", scratch, len(local.opened))
	}
	if !strings.Contains(local.got.SystemPrompt, "carries a `path` instead of a `body`") {
		t.Error("the performer's briefing must say a doc may come as a path")
	}

	remote := pathlessRunner{&fakeRunner{out: budgetEnvelope(t, 1)}}
	assertRefused(t, coderDispatch(t, inj, remote, "story body", docs), "coder", "in_progress", 15000)
	unbudgeted := pathlessRunner{&fakeRunner{out: budgetEnvelope(t, 1)}}
	if err := coderDispatch(t, config.InjectedConfig{}, unbudgeted, "story body", docs); err != nil || !strings.Contains(unbudgeted.got.Payload, "PLAN-SENTINEL") {
		t.Errorf("a performer that cannot read local paths keeps the plan inline: %v", err)
	}
}

func summariseUnder(t *testing.T, inj config.InjectedConfig, bodyBytes int) (*scriptedRunner, error) {
	t.Helper()
	docs := fakeDocs{workflow: summaryWorkflow, skillBody: "summarise rubric", skillFound: true}
	r := &scriptedRunner{results: []struct {
		out string
		err error
	}{{out: "the step recap"}}}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	g.SetInjected(inj, condense.Rules{})
	_, err := g.Summarise(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress", Body: strings.Repeat("s", bodyBytes)}, "in_progress", "done")
	return r, err
}

func reviewTextUnder(t *testing.T, inj config.InjectedConfig, textBytes int) (*textRunner, error) {
	t.Helper()
	r := &textRunner{reply: "ok"}
	g := reviewTextEngine(r)
	g.SetInjected(inj, condense.Rules{})
	_, err := g.ReviewText(context.Background(), ReviewTextRequest{
		Section: config.RoleReviewer,
		Binding: config.AgentBinding{Role: config.RoleReviewer, Command: reviewerCmd, Tools: "Read,Grep,Glob"},
		Skill:   "satelle-context-audit", Rubric: "RUBRIC", Payload: map[string]string{"text": strings.Repeat("t", textBytes)},
	})
	return r, err
}

func TestDispatchPayloadBudget(t *testing.T) {
	const big, small = 20000, 10
	// The fixture budgets sit between what a small payload and a big one send,
	// so each case can only pass by measuring the payload.
	t.Run("one-shot dispatch", func(t *testing.T) {
		r, err := oneShotDispatch(t, budgetFor("coder", 15000), big)
		assertRefused(t, err, "coder", "in_progress", 15000)
		if r.got.SystemPrompt != "" || r.got.Payload != "" {
			t.Error("an over-budget dispatch must start no run")
		}
		if _, err := oneShotDispatch(t, budgetFor("coder", 15000), small); err != nil {
			t.Errorf("a small payload under the same budget must dispatch: %v", err)
		}
		if r, err := oneShotDispatch(t, config.InjectedConfig{}, big); err != nil || !strings.Contains(r.got.Payload, strings.Repeat("b", big)) {
			t.Errorf("a seat with no budget row is unmeasured and sends its payload whole: %v", err)
		}
	})
	t.Run("summariser", func(t *testing.T) {
		r, err := summariseUnder(t, budgetFor("reviewer", 15000), big)
		assertRefused(t, err, "reviewer", "summarise", 15000)
		if r.calls != 0 {
			t.Errorf("an over-budget summary must start no run, got %d", r.calls)
		}
		if _, err := summariseUnder(t, budgetFor("reviewer", 15000), small); err != nil {
			t.Errorf("a small summary payload must dispatch: %v", err)
		}
		if _, err := summariseUnder(t, config.InjectedConfig{}, big); err != nil {
			t.Errorf("no budget row: the summary dispatches whole: %v", err)
		}
	})
	t.Run("review text", func(t *testing.T) {
		r, err := reviewTextUnder(t, budgetFor("reviewer", 15000), big)
		assertRefused(t, err, "reviewer", "satelle-context-audit", 15000)
		if len(r.reqs) != 0 {
			t.Errorf("an over-budget review must start no run, got %d", len(r.reqs))
		}
		if _, err := reviewTextUnder(t, budgetFor("reviewer", 15000), small); err != nil {
			t.Errorf("a small review payload must dispatch: %v", err)
		}
		if _, err := reviewTextUnder(t, config.InjectedConfig{}, big); err != nil {
			t.Errorf("no budget row: the review dispatches whole: %v", err)
		}
	})
}

// TestCheckDispatchBudgetCountsBothParts: the measure is the system prompt plus
// the payload, so either alone under the budget can still be over it together,
// and a live session (OpenSessionAs) names its step by status and role.
func TestCheckDispatchBudgetCountsBothParts(t *testing.T) {
	g := New(nil, nil, "", "")
	g.SetInjected(budgetFor("reviewer-consult", 100), condense.Rules{})
	inv := invocation{section: "reviewer-consult", step: liveSessionStep(SessionRoleConsult, "in_progress")}
	if err := g.checkDispatchBudget(inv, 60, 40); err != nil {
		t.Errorf("exactly at budget passes: %v", err)
	}
	err := g.checkDispatchBudget(inv, 60, 41)
	assertRefused(t, err, "reviewer-consult", "in_progress (consult session)", 100)
	if !strings.Contains(err.Error(), "system prompt 60 + payload 41") {
		t.Errorf("refusal must name both sizes: %v", err)
	}
	if err := g.checkDispatchBudget(invocation{step: "x"}, 1e6, 1e6); err != nil {
		t.Errorf("an invocation naming no seat is unmeasured: %v", err)
	}
}

// TestSeatFittingLeavesRoomForThePayload: the seat's budget covers the system
// prompt and the payload together, so the resident principles are fitted to
// what the payload leaves — a payload that still fits beside the condensed
// prompt dispatches rather than tripping the backstop.
func TestSeatFittingLeavesRoomForThePayload(t *testing.T) {
	g, _, _ := seatEngine(t, 0)
	full, err := g.buildRequest(context.Background(), invocation{
		charter: reviewerCharter(), rubric: "RUBRIC-BODY", principles: config.PrinciplesSession,
		payload: map[string]string{}, section: "reviewer", storyID: "sty_seat",
	})
	if err != nil {
		t.Fatal(err)
	}
	budget := len(full.SystemPrompt)
	fitted, _, _ := seatEngine(t, budget)
	payload := map[string]string{"k": strings.Repeat("p", 600)}
	req, err := fitted.buildRequest(context.Background(), invocation{
		charter: reviewerCharter(), rubric: "RUBRIC-BODY", principles: config.PrinciplesSession,
		payload: payload, section: "reviewer", storyID: "sty_seat",
	})
	if err != nil {
		t.Fatalf("a payload the condensed prompt leaves room for must dispatch: %v", err)
	}
	if got := len(req.SystemPrompt) + len(req.Payload); got > budget {
		t.Errorf("prompt %d + payload %d = %d exceeds the seat budget %d", len(req.SystemPrompt), len(req.Payload), got, budget)
	}
	if !strings.Contains(req.Payload, strings.Repeat("p", 600)) {
		t.Error("the payload is sent whole, never trimmed")
	}
}

// TestLiveSessionDocumentsGoByPath (sty_73a2dc68): a live session — the rework
// consultant here — runs on this machine, so it too is sent each document as a
// path into its scratch, and a plan larger than its budget opens the session.
func TestLiveSessionDocumentsGoByPath(t *testing.T) {
	plan := DocState{Name: "plan", Type: "plan", Body: "PLAN-SENTINEL " + strings.Repeat("p", 30000)}
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		return config.AgentBinding{Interface: "stream", Tools: "Read", Command: "claude -p {tools}"}, name == "reviewer-consult"
	})
	g.SetDocsResolver(func(context.Context, string) []DocState { return []DocState{plan} })
	g.SetInjected(budgetFor("reviewer-consult", 15000), condense.Rules{})
	var got agentcli.Request
	var opened map[string][]byte
	g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
		return func(_ context.Context, req agentcli.Request, _ agentcli.PermissionPolicy) (agentcli.Session, error) {
			got, opened = req, openPayloadFiles(req.Payload)
			return closedSess{}, nil
		}, nil
	}
	sess, err := g.OpenSessionAsWithModel(context.Background(), "reviewer-consult", SessionRoleConsult,
		workitem.Item{ID: "sty_live", Status: "in_progress"}, nil, nil, "")
	if err != nil {
		t.Fatalf("a consult whose plan goes by path must open under its budget: %v", err)
	}
	defer func() { _ = sess.Close() }()
	if strings.Contains(got.Payload, "PLAN-SENTINEL") {
		t.Errorf("a live session must be sent the plan's path, not its body:\n%s", got.Payload)
	}
	var read bool
	for _, b := range opened {
		read = read || string(b) == plan.Body
	}
	if !read {
		t.Errorf("the payload's doc path must hold the full plan; opened %d file(s)", len(opened))
	}
}
