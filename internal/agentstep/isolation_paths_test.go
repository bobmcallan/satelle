package agentstep

import (
	"context"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// One isolation path for every role=reviewer dispatch (sty_ef3efb51): the step
// summariser and the rework consultant's live session are refused, trimmed and
// recorded exactly like a gate verdict.

const summaryRO = "Read,Grep,Glob"

func summariseWith(t *testing.T, iface, command, tools string) (*[]telemetryRec, error) {
	t.Helper()
	runner, err := agentcli.RunnerFromBinding(iface, command)
	if err != nil {
		t.Fatal(err)
	}
	docs := fakeDocs{workflow: summaryWorkflow, skillBody: "summarise rubric", skillFound: true}
	g := New(runner, docs, t.TempDir(), "")
	g.SetReviewerTools(tools)
	recs := captureTelemetry(g)
	_, serr := g.Summarise(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "in_progress", "done")
	return recs, serr
}

// A refused summariser binding starts no process and names the adapter and class.
func TestSummarise_RefusedBindingStartsNoProcess(t *testing.T) {
	cases := []struct {
		name, iface, binary, args, wantAdapter string
	}{
		{"grok acp stock spawn", "acp", "grok-shim", "agent stdio", "grok/acp"},
		{"grok command always-approve, no --tools", "command", "grok-shim", "-p {payload} --always-approve", "grok/command"},
		{"grok command always-approve, blank --tools", "command", "grok-shim", "-p {payload} --always-approve --tools=", "grok/command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, marker := startCounter(t, tc.binary, "the recap")
			recs, err := summariseWith(t, tc.iface, path+" "+tc.args, summaryRO)
			if err == nil {
				t.Fatal("a refused summariser binding must surface an error for a mandatory summary")
			}
			msg := err.Error()
			if !strings.Contains(msg, "refused") || !strings.Contains(msg, tc.wantAdapter) || !strings.Contains(msg, "shell") {
				t.Errorf("refusal %q must name the adapter %q and the tool class", msg, tc.wantAdapter)
			}
			if n := starts(marker); n != 0 {
				t.Fatalf("a refused summariser started %d process(es)", n)
			}
			var refused bool
			for _, r := range *recs {
				refused = refused || r.kind == "reviewer-isolation-refused"
			}
			if !refused {
				t.Errorf("no reviewer-isolation-refused event: %+v", *recs)
			}
		})
	}
}

// A stock claude summariser still runs, once, and its result carries the offered
// tools it was trimmed to.
func TestSummarise_StockClaudeRunsAndRecordsOfferedTools(t *testing.T) {
	path, marker := startCounter(t, "claude-shim", "the recap")
	command := strings.Replace(agentcli.DefaultClaudeCommand, "claude ", path+" ", 1)
	runner, err := agentcli.RunnerFromBinding("command", command)
	if err != nil {
		t.Fatal(err)
	}
	docs := fakeDocs{workflow: summaryWorkflow, skillBody: "summarise rubric", skillFound: true}
	g := New(runner, docs, t.TempDir(), "")
	g.SetReviewerTools(summaryRO)
	got, serr := g.Summarise(context.Background(), workitem.Item{ID: "sty_1", Status: "in_progress"}, "in_progress", "done")
	if serr != nil {
		t.Fatalf("stock claude summariser refused: %v", serr)
	}
	if n := starts(marker); n != 1 {
		t.Fatalf("start count = %d, want 1", n)
	}
	if got.OfferedToolCount == nil || *got.OfferedToolCount != 3 || got.OfferedToolsSource != agentcli.OfferedSourceFlag {
		t.Fatalf("offered tools = %v source %q, want 3 from the flag", got.OfferedToolCount, got.OfferedToolsSource)
	}
}

type liveReviewer struct {
	opens  int
	req    agentcli.Request
	pol    agentcli.PermissionPolicy
	rows   []map[string]any
	events *[]telemetryRec
}

// openConsult opens a role=reviewer consult session over a fake opener and
// reports what the opener saw.
func openConsult(t *testing.T, iface, command, tools string, caller agentcli.PermissionPolicy) (*liveReviewer, error) {
	t.Helper()
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Role: config.RoleReviewer, Interface: iface, Command: command, Tools: tools}, true
	})
	lr := &liveReviewer{events: captureTelemetry(g)}
	g.SetInvocationRecorder(func(_ context.Context, _ string, p map[string]any) error {
		lr.rows = append(lr.rows, p)
		return nil
	})
	g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
		return func(_ context.Context, req agentcli.Request, pol agentcli.PermissionPolicy) (agentcli.Session, error) {
			lr.opens++
			lr.req, lr.pol = req, pol
			return closedSess{}, nil
		}, nil
	}
	sess, err := g.OpenSessionAsWithModel(context.Background(), "reviewer-consult", SessionRoleConsult,
		workitem.Item{ID: "sty_live", Status: "in_progress"}, caller, nil, "")
	if err == nil {
		_ = sess.Close()
	}
	return lr, err
}

// A refused consult binding opens no session and names the adapter.
func TestOpenSession_RefusedReviewerConsultOpensNothing(t *testing.T) {
	cases := []struct {
		name, iface, command, tools, wantAdapter string
	}{
		{"grok acp stock spawn", "acp", "grok agent stdio", summaryRO, "grok/acp"},
		{"grok acp always-approve", "acp", "grok agent --always-approve stdio", summaryRO, "grok/acp"},
		{"claude stream bypass + scoped grant", "stream", "claude -p --tools {tools} --permission-mode bypassPermissions", summaryRO + ",Bash(satelle:*)", "claude/stream"},
		{"unrecognised harness", "acp", "mystery-cli serve", summaryRO, "unknown/acp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lr, err := openConsult(t, tc.iface, tc.command, tc.tools, nil)
			if err == nil {
				t.Fatal("a refused consult binding opened a session")
			}
			if msg := err.Error(); !strings.Contains(msg, "refused") || !strings.Contains(msg, tc.wantAdapter) {
				t.Errorf("refusal %q must name the adapter %q", msg, tc.wantAdapter)
			}
			if lr.opens != 0 {
				t.Fatalf("a refused consult binding started %d session(s)", lr.opens)
			}
			var refused bool
			for _, r := range *lr.events {
				refused = refused || r.kind == "reviewer-isolation-refused"
			}
			if !refused {
				t.Errorf("no reviewer-isolation-refused event: %+v", *lr.events)
			}
			if len(lr.rows) != 0 {
				t.Errorf("a refused open wrote an invocation row: %+v", lr.rows)
			}
		})
	}
}

// A stock claude stream consult still opens: the request is read-only, the open
// row records the offered tools, and the caller's own policy is narrowed by the
// grant, never widened.
func TestOpenSession_StockClaudeConsultRunsReadOnlyAndRecords(t *testing.T) {
	var callerSaw int
	caller := func(agentcli.PermissionRequest) agentcli.PermissionDecision {
		callerSaw++
		return agentcli.PermissionDecision{Allow: true}
	}
	lr, err := openConsult(t, "stream", agentcli.DefaultClaudeStreamCommand, summaryRO, caller)
	if err != nil {
		t.Fatalf("stock claude consult refused: %v", err)
	}
	if lr.opens != 1 || !lr.req.ReadOnly {
		t.Fatalf("opens = %d, ReadOnly = %v; want one read-only open", lr.opens, lr.req.ReadOnly)
	}
	var open map[string]any
	for _, r := range lr.rows {
		if r["phase"] == "open" {
			open = r
		}
	}
	if open == nil || open["offered_tool_count"] != 3 || open["offered_tools_source"] != agentcli.OfferedSourceFlag {
		t.Fatalf("open row = %+v, want offered_tool_count 3 from the flag", open)
	}
	if lr.pol(agentcli.PermissionRequest{ToolName: "Read", Kind: "read"}).Allow != true {
		t.Error("a granted read tool must be allowed")
	}
	if callerSaw == 0 {
		t.Error("the caller's own policy must still be consulted")
	}
	for _, tool := range []string{"Bash", "Write", "Edit", "WebFetch", "mcp__x__y"} {
		if lr.pol(agentcli.PermissionRequest{ToolName: tool}).Allow {
			t.Errorf("tool %q outside the grant was allowed although the caller's policy allows everything", tool)
		}
	}
}

// A role=agent live session is not a reviewer dispatch: no preflight, no trim.
func TestOpenSession_PerformerSessionIsNotIsolated(t *testing.T) {
	g, _ := newEngine(t, "", fakeDocs{})
	g.SetNamedAgents(func(string) (config.AgentBinding, bool) {
		return config.AgentBinding{Role: config.RoleAgent, Interface: "acp", Command: "grok agent stdio", Tools: "Read,Edit"}, true
	})
	var req agentcli.Request
	g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
		return func(_ context.Context, r agentcli.Request, _ agentcli.PermissionPolicy) (agentcli.Session, error) {
			req = r
			return closedSess{}, nil
		}, nil
	}
	sess, err := g.OpenSessionAs(context.Background(), "coder", SessionRoleDriving,
		workitem.Item{ID: "sty_live", Status: "in_progress"}, nil, nil)
	if err != nil {
		t.Fatalf("performer session refused: %v", err)
	}
	_ = sess.Close()
	if req.ReadOnly {
		t.Error("a performer session must not be marked read-only")
	}
}
