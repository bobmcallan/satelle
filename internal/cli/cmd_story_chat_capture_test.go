package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// wireLedgerOnly opens a temp store and wires only the ledger into the verb
// registry — orchestratorModelCapture's dependencies (verb.RecordSessionModel
// / verb.SessionModels) need nothing else.
func wireLedgerOnly(t *testing.T) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "satelle.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	verb.SetLedgerStore(db.Ledger)
	t.Cleanup(func() {
		db.Close()
		verb.SetLedgerStore(nil)
	})
}

// TestOrchestratorModelCaptureRecordsKnownModelOnce pins the `story chat`
// orchestrator capture (sty_7069bced AC4): the FIRST EventSessionInit records
// the reported model under the orchestrator role, a later init is a no-op
// (one-shot capture — EventHandler may be invoked from concurrent
// stdout/stderr reader goroutines), and every event still reaches base.
func TestOrchestratorModelCaptureRecordsKnownModelOnce(t *testing.T) {
	wireLedgerOnly(t)
	var forwarded []agentcli.EventKind
	base := func(ev agentcli.Event) { forwarded = append(forwarded, ev.Kind) }
	handler, closeUnrecorded := orchestratorModelCapture(context.Background(), "sty_chat1", "sess-1", "claude", base)

	handler(agentcli.Event{Kind: agentcli.EventSessionInit, Model: "claude-opus-5-5"})
	handler(agentcli.Event{Kind: agentcli.EventSessionInit, Model: "claude-sonnet-5"}) // must not overwrite
	handler(agentcli.Event{Kind: agentcli.EventUsage})
	closeUnrecorded() // init already recorded — must be a no-op

	orch, _, _ := verb.SessionModels(context.Background(), "sty_chat1")
	if orch.Model != "claude-opus-5-5" || orch.Executable != "claude" {
		t.Fatalf("orchestrator session model = %+v, want claude-opus-5-5/claude", orch)
	}
	if len(forwarded) != 3 {
		t.Fatalf("forwarded = %v, want every event forwarded to base", forwarded)
	}
}

// TestOrchestratorModelCaptureRecordsUnknownWhenSessionNeverInits pins the
// close-time fallback: a session that closes with no EventSessionInit (ACP,
// or a stream transport that never reached the init line) records "unknown"
// rather than leaving the orchestrator role silently unresolved.
func TestOrchestratorModelCaptureRecordsUnknownWhenSessionNeverInits(t *testing.T) {
	wireLedgerOnly(t)
	handler, closeUnrecorded := orchestratorModelCapture(context.Background(), "sty_chat2", "sess-2", "claude", func(agentcli.Event) {})
	handler(agentcli.Event{Kind: agentcli.EventUsage}) // never an init
	closeUnrecorded()

	orch, _, _ := verb.SessionModels(context.Background(), "sty_chat2")
	if orch.Model != "unknown" {
		t.Fatalf("orchestrator session model = %+v, want unknown", orch)
	}
}
