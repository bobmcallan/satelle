package verb

// The configuration stamp on agent_invocation rows (sty_9914df10 AC2): rows
// written through the reviewer builder (invocationPayload) and the executor
// builder (dispatchPayload) carry the substrate and bindings digests, and the
// substrate digest follows an edit to an authored document.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/sessionconfig"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func writeAuthored(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type stampPair struct{ substrate, bindings string }

func stampedPayload(t *testing.T, e ledger.Entry) stampPair {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatal(err)
	}
	s, _ := p[sessionconfig.KeySubstrate].(string)
	b, _ := p[sessionconfig.KeyBindings].(string)
	return stampPair{s, b}
}

func TestAgentInvocationRowsCarryTheConfigurationDigests(t *testing.T) {
	db := wireDU(t)
	ctx := context.Background()
	root := t.TempDir()
	SetStoryDir(filepath.Join(root, "stories"))
	data := filepath.Join(root, ".satelle")
	roots := map[string]string{
		"skills":     filepath.Join(data, "skills"),
		"workflows":  filepath.Join(data, "workflows"),
		"principles": filepath.Join(data, "principles"),
		"documents":  filepath.Join(data, "documents"),
	}
	skill := filepath.Join(roots["skills"], "alpha.md")
	writeAuthored(t, skill, "# Alpha\n\nfirst body\n")
	writeAuthored(t, filepath.Join(roots["documents"], "notes.md"), "documents are not configuration\n")
	db.DocIndex.SetRoots(roots)
	sync := func() {
		t.Helper()
		if _, err := db.DocIndex.Sync(ctx, roots, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	sync()

	src := &sessionconfig.Source{
		Docs: db.DocIndex, Roots: roots,
		Bindings: func() ([]byte, error) {
			return sessionconfig.BindingsJSON(config.AgentsConfig{Executor: config.AgentBinding{Command: "in-loop"}})
		},
	}
	db.Ledger.SetInvocationStamper(src.Stamp)

	it, err := db.Stories.Create(ctx, workitem.CreateInput{Kind: workitem.KindStory, Title: "stamped", Status: "backlog"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	write := func() {
		t.Helper()
		now := time.Now()
		appendLedgerEntry(ctx, it.ID, ledger.KindAgentInvocation, "reviewer", "invoked reviewer",
			invocationPayload("plan", "in_progress", "1", ReviewerVerdict{Skill: "x", Command: "claude"}), now)
		appendLedgerEntry(ctx, it.ID, ledger.KindAgentInvocation, "executor", "dispatched step",
			dispatchPayload("in_progress", "integration", DispatchResult{Agent: "coder", Command: "grok", Skill: "coder"}, "1"), now)
	}
	rows := func() []ledger.Entry {
		t.Helper()
		got, err := db.Ledger.ListByStory(ctx, it.ID, ledger.KindAgentInvocation)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	write()
	first := rows()
	if len(first) != 2 {
		t.Fatalf("want a reviewer and an executor row, got %d", len(first))
	}
	before := stampedPayload(t, first[0])
	for i, e := range first {
		s := stampedPayload(t, e)
		if s.substrate == "" || s.bindings == "" {
			t.Fatalf("row %d (%s) is not stamped: %s", i, e.Actor, e.Payload)
		}
		if s != before {
			t.Errorf("row %d: both builders must stamp the same configuration, got %v and %v", i, s, before)
		}
	}

	// A .local file is never configuration: adding one leaves the digest alone.
	writeAuthored(t, filepath.Join(roots["skills"], "x.local.md"), "# machine only\n")
	sync()
	write()
	if s := stampedPayload(t, rows()[2]); s != before {
		t.Errorf("a .local skill changed the stamp: %v vs %v", s, before)
	}

	// Editing one skill body, then re-syncing the index, changes the digest.
	writeAuthored(t, skill, "# Alpha\n\nsecond body\n")
	sync()
	write()
	all := rows()
	after := stampedPayload(t, all[4])
	if after.substrate == before.substrate {
		t.Error("editing a skill must change substrate_digest")
	}
	if after.bindings != before.bindings {
		t.Error("an unchanged bindings set must keep bindings_digest")
	}

	// One bundle per distinct pair, named by it.
	for _, s := range []stampPair{before, after} {
		name := sessionconfig.BundleName(s.substrate, s.bindings)
		if !HasStoryDoc(ctx, it.ID, name) {
			t.Errorf("story is missing its configuration bundle %s", name)
		}
	}
}
