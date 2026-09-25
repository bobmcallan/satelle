package agentstep

import (
	"context"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestLiveSession_LedgersInteractiveDenied pins sty_ff50f788 AC1: a live
// session (stream and ACP alike — the transports emit the same event) whose
// agent asks the user writes an agent-interactive-denied row with tool,
// question and response as it happens, and still forwards the event.
func TestLiveSession_LedgersInteractiveDenied(t *testing.T) {
	for _, tc := range []struct {
		name, iface, tool, question, response string
	}{
		{"stream", "stream", "AskUserQuestion", "which story?", "denied"},
		{"acp", "acp", "Ask: which story?", "which story?", "denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := newEngine(t, "", fakeDocs{})
			g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
				return config.AgentBinding{Interface: tc.iface, Tools: "Read", Command: "agent"}, true
			})
			var recs []telemetryRec
			g.SetTelemetry(func(_ context.Context, storyID, actor, kind string, data map[string]any) {
				recs = append(recs, telemetryRec{storyID, actor, kind, data})
			})
			g.newOpener = func(string, string) (agentcli.SessionOpener, error) {
				return func(_ context.Context, req agentcli.Request, _ agentcli.PermissionPolicy) (agentcli.Session, error) {
					ev := agentcli.Event{Kind: agentcli.EventInteractiveDenied, Tool: tc.tool, Text: tc.question,
						Meta: map[string]string{agentcli.EventMetaResponse: tc.response}}
					req.OnEvent(ev)
					return closedSess{}, nil
				}, nil
			}
			var forwarded int
			sess, err := g.OpenSessionAsWithModel(context.Background(), "orchestrator", SessionRoleDriving,
				workitem.Item{ID: "sty_live", Status: "in_progress"}, nil,
				func(ev agentcli.Event) {
					if ev.Kind == agentcli.EventInteractiveDenied {
						forwarded++
					}
				}, "")
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			_ = sess.Close()
			var rows []telemetryRec
			for _, r := range recs {
				if r.kind == "agent-interactive-denied" {
					rows = append(rows, r)
				}
			}
			if len(rows) != 1 {
				t.Fatalf("agent-interactive-denied rows = %d, want 1: %#v", len(rows), recs)
			}
			r := rows[0]
			if r.storyID != "sty_live" || r.data["tool"] != tc.tool || r.data["question"] != tc.question || r.data["response"] != tc.response {
				t.Fatalf("row = %#v", r)
			}
			if forwarded != 1 {
				t.Fatalf("caller handler saw %d denied events, want 1", forwarded)
			}
		})
	}
}
