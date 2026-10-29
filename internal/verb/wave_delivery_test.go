package verb_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_c931cd90: `story wave` prints how each open child will be delivered — its
// order, lane, the step that delivers it and where that step runs (the engine's
// own answer), and the orchestrator's reason as the readiness round recorded it.

// deliveryWF is waveWFLine's shape with the container's ready step first on its
// route, as the shipped epic route has it, and readyLines/codedLines authored
// verbatim on the container's ready step and the working lane's coded step.
func deliveryWF(readyLines, codedLines string) map[string]string {
	return routeHalves(
		`["*"]
obligations = ["raised", "coded", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }

[epic-parent]
obligations = ["raised", "ready", "epic-closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`,
		`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
waits_on_children = true
`+readyLines+`requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
`+codedLines+`requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]

[epic-closed]
status = "done"
terminal = true
requires = ["ready"]
`)
}

// summaryGate is the always-on step-summary gate as this repo declares it: an
// edge-less node bound to a role=reviewer seat, so it sits on the route's spine.
const summaryGate = `
[[gate]]
skill = "satelle-step-summary"
agent = "reviewer-summary"
mandatory = true
for = ["*", "epic-parent"]
`

// waveResolver is the wired performer rule. The container's ready step is
// performed by a dispatched agent whose skill names the document "ready"; a
// child's `in_progress` step answers by the child's placement pin, the way the
// engine does, and an unpinned child falls to an in-loop [executor]. Asked for
// the step-summary node, it refuses it the way the engine refuses a reviewer on
// a performing state.
type waveResolver struct{}

func (waveResolver) DispatchExecutor(context.Context, workitem.Item, string) (verb.DispatchResult, error) {
	return verb.DispatchResult{}, nil
}

func (waveResolver) StepPerformer(_ context.Context, it workitem.Item, to string) (verb.PerformerResolution, error) {
	switch to {
	case "gate_satelle-step-summary":
		return verb.PerformerResolution{}, errors.New(`workflow "default" allocates performing state "gate_satelle-step-summary" to agent "reviewer-summary" with role=reviewer`)
	case "ready":
		return verb.PerformerResolution{Kind: verb.PerformerDispatched, Dispatched: true, Agent: "ready-reviewer", OutputName: "ready"}, nil
	case "in_progress":
		for _, tag := range it.Tags {
			switch tag {
			case "placement:in-loop":
				return verb.PerformerResolution{Kind: verb.PerformerInLoop, Agent: "coder"}, nil
			case "placement:local":
				return verb.PerformerResolution{Kind: verb.PerformerDispatched, Dispatched: true, Agent: "coder"}, nil
			case "placement:team:/teams/x":
				return verb.PerformerResolution{Kind: verb.PerformerDispatched, Dispatched: true, Agent: "coder", Team: "/teams/x"}, nil
			case "placement:remote":
				return verb.PerformerResolution{}, context.DeadlineExceeded
			case "placement:unbound":
				return verb.PerformerResolution{}, errors.New(`step "in_progress" names agent "ghost" but agents.toml defines no [ghost] binding`)
			}
		}
		return verb.PerformerResolution{Kind: verb.PerformerExecutor, Agent: "executor", ExecutorInLoop: true}, nil
	}
	return verb.PerformerResolution{Kind: verb.PerformerNone}, nil
}

func deliveryByID(t *testing.T, res verb.WaveResult, id string) verb.WaveDelivery {
	t.Helper()
	for _, d := range res.Delivery {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("no delivery line for %s in %+v", id, res.Delivery)
	return verb.WaveDelivery{}
}

func TestWaveReportsRecordedDelivery(t *testing.T) {
	wireWithWorkflows(t, deliveryWF(
		`delivery_heading = "Delivery"`+"\n"+`lane_tag = "lane:"`+"\n",
		`placement_tag = "placement:"`+"\n"))
	verb.SetStoryDir(t.TempDir())
	verb.SetExecutorDispatcher(waveResolver{})

	epic := waveEpic(t)
	plain := waveChild(t)
	laned := waveChild(t, "lane:trunk", "placement:team:/teams/x")
	after := waveChild(t, "depends-on:"+plain.ID, "placement:in-loop")
	local := waveChild(t, "placement:local")
	call(t, "story-doc-attach", map[string]any{
		"story_id": epic.ID, "name": "ready", "type": "review",
		"body": "parallel: all\n\n## Delivery\n" +
			plain.ID + ": small, reuses the driving session's context\n" +
			"- " + laned.ID + ": long-running, wants its own machine\n" +
			after.ID + ": reads the first child's output\n\n## Other\n" + local.ID + ": not a delivery line\n",
	})

	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	for _, c := range []struct {
		id                                       workitem.Item
		order, lane, step, placement, agent, why string
	}{
		{plain, "parallel", "", "in_progress", "executor (in-loop)", "executor", "small, reuses the driving session's context"},
		{laned, "parallel", "trunk", "in_progress", "team:/teams/x", "coder", "long-running, wants its own machine"},
		{after, "after " + plain.ID, "", "in_progress", "in-loop", "coder", "reads the first child's output"},
		{local, "parallel", "", "in_progress", "dispatched", "coder", ""},
	} {
		d := deliveryByID(t, res, c.id.ID)
		if d.Order != c.order || d.Lane != c.lane || d.Step != c.step || d.Placement != c.placement || d.Agent != c.agent || d.Reason != c.why || d.Error != "" {
			t.Errorf("%s delivery = %+v, want order %q lane %q step %q placement %q agent %q reason %q",
				c.id.ID, d, c.order, c.lane, c.step, c.placement, c.agent, c.why)
		}
	}
	if len(res.Delivery) != 4 {
		t.Errorf("want one delivery per open child, got %+v", res.Delivery)
	}
}

// A pin the engine cannot honour is the engine's error for that child, shown on
// its line; the others still print.
func TestWaveDeliveryShowsAResolverErrorPerChild(t *testing.T) {
	wireWithWorkflows(t, deliveryWF("", `placement_tag = "placement:"`+"\n"))
	verb.SetExecutorDispatcher(waveResolver{})
	epic := waveEpic(t)
	bad := waveChild(t, "placement:remote")
	ok := waveChild(t)

	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	if d := deliveryByID(t, res, bad.ID); d.Error == "" || d.Placement != "" || d.Step != "in_progress" {
		t.Errorf("bad child = %+v, want an error on step in_progress and no placement", d)
	}
	if d := deliveryByID(t, res, ok.ID); d.Error != "" || d.Placement != "executor (in-loop)" {
		t.Errorf("ok child = %+v, want its placement", d)
	}
}

// The step-summary gate node judges and has no performer: the wave passes over
// it, so each child's delivery is its first performing step with no error,
// while a performing step the engine really cannot resolve still reports.
func TestWaveDeliverySkipsSummariserNode(t *testing.T) {
	wf := deliveryWF("", "")
	wf["step"] += summaryGate
	wireWithWorkflows(t, wf)
	verb.SetExecutorDispatcher(waveResolver{})
	epic := waveEpic(t)
	plain := waveChild(t)
	pinned := waveChild(t, "placement:local")
	unbound := waveChild(t, "placement:unbound")

	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	for _, c := range []struct {
		id               workitem.Item
		placement, agent string
	}{
		{plain, "executor (in-loop)", "executor"},
		{pinned, "dispatched", "coder"},
	} {
		if d := deliveryByID(t, res, c.id.ID); d.Error != "" || d.Step != "in_progress" || d.Placement != c.placement || d.Agent != c.agent {
			t.Errorf("%s delivery = %+v, want step in_progress placement %q agent %q and no error", c.id.ID, d, c.placement, c.agent)
		}
	}
	if d := deliveryByID(t, res, unbound.ID); !strings.Contains(d.Error, "no [ghost] binding") || d.Step != "in_progress" || d.Placement != "" {
		t.Errorf("unbound child = %+v, want the binding error on step in_progress and no placement", d)
	}
}

// A route whose ready step declares no delivery_heading or lane_tag gives empty
// reasons and no lane, and no error; with no resolver wired the placement fields
// are empty.
func TestWaveDeliveryWithoutHeadingOrResolver(t *testing.T) {
	wireWithWorkflows(t, deliveryWF("", ""))
	verb.SetStoryDir(t.TempDir())
	verb.SetExecutorDispatcher(waveResolver{})
	epic := waveEpic(t)
	child := waveChild(t, "lane:trunk")
	call(t, "story-doc-attach", map[string]any{
		"story_id": epic.ID, "name": "ready", "type": "review",
		"body": "## Delivery\n" + child.ID + ": a reason nobody asked to record\n",
	})
	res, err := wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	if d := deliveryByID(t, res, child.ID); d.Reason != "" || d.Lane != "" || d.Placement == "" {
		t.Errorf("delivery = %+v, want no reason and no lane, with a placement", d)
	}

	verb.SetExecutorDispatcher(nil)
	res, err = wave(t, epic.ID)
	if err != nil {
		t.Fatalf("wave: %v", err)
	}
	if d := deliveryByID(t, res, child.ID); d.Placement != "" || d.Step != "" || !strings.HasPrefix(d.Order, "parallel") {
		t.Errorf("delivery = %+v, want only the order with no resolver wired", d)
	}
}
