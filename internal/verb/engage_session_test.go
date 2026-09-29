package verb

// sty_a7914904 AC1: at engage, a driving session whose driver_usage rows already
// cover another story is told to start a fresh session — with no budget
// configured — and a repo-configured context budget is a further trigger. It is
// a warning only: the transition always succeeds.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/workitem"
)

type engageHarness struct {
	t     *testing.T
	db    *store.DB
	lines []string
}

func newEngageHarness(t *testing.T, session string) *engageHarness {
	t.Helper()
	h := &engageHarness{t: t, db: wireDU(t)}
	if session != "" {
		t.Setenv(config.SessionEnv, session)
		t.Setenv("CLAUDECODE", "1")
	}
	stubSnapshotter(t, agentcli.DriverSnapshot{Available: true, Model: "claude-opus-5-5",
		FreshInputTokens: 100, CacheReadInputTokens: 50, CacheCreationInputTokens: 10, OutputTokens: 10, Turns: 1})
	SetVerdictRecorder(func(s string) { h.lines = append(h.lines, s) })
	t.Cleanup(func() { SetVerdictRecorder(nil); SetAgentBudgets(nil) })
	return h
}

func (h *engageHarness) dispatch(name string, req map[string]any) workitem.Item {
	h.t.Helper()
	b, _ := json.Marshal(req)
	resp, err := Dispatch(context.Background(), name, b)
	if err != nil {
		h.t.Fatalf("%s %v: %v", name, req, err)
	}
	var it workitem.Item
	if err := json.Unmarshal(resp, &it); err != nil {
		h.t.Fatal(err)
	}
	return it
}

func (h *engageHarness) create(title string) workitem.Item {
	return h.dispatch("story-create", map[string]any{"title": title, "status": "backlog",
		"tags": []string{"estimate-minutes:10", "estimate-tokens:1000"}})
}

func (h *engageHarness) set(id, status string) workitem.Item {
	it := h.dispatch("story-set", map[string]any{"id": id, "status": status})
	if it.Status != status {
		h.t.Fatalf("story-set %s → %s: status = %q", id, status, it.Status)
	}
	return it
}

func (h *engageHarness) advisories(id string) []SessionAdvisoryPayload {
	h.t.Helper()
	entries, err := h.db.Ledger.ListByStory(context.Background(), id, ledger.KindSessionAdvisory)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []SessionAdvisoryPayload
	for _, e := range entries {
		var p SessionAdvisoryPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func (h *engageHarness) warned(needle string) bool {
	for _, l := range h.lines {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

// (a) another story's rows in this session → warning + one advisory row, with
// NO budget configured, and the engage succeeds.
func TestEngageWarnsWhenSessionCoversAnotherStory(t *testing.T) {
	h := newEngageHarness(t, "sess-fresh-1")
	first := h.create("first story")
	h.set(first.ID, "in_progress")
	h.set(first.ID, "blocked")

	second := h.create("second story")
	h.lines = nil
	h.set(second.ID, "in_progress") // asserts the engage succeeded

	adv := h.advisories(second.ID)
	if len(adv) != 1 {
		t.Fatalf("session_advisory rows = %d, want 1: %+v", len(adv), adv)
	}
	if adv[0].Trigger != SessionTriggerOtherStory || len(adv[0].OtherStories) != 1 || adv[0].OtherStories[0] != first.ID {
		t.Errorf("advisory = %+v, want other-story naming %s", adv[0], first.ID)
	}
	if adv[0].SessionID != "sess-fresh-1" {
		t.Errorf("advisory session = %q", adv[0].SessionID)
	}
	if !h.warned("start a fresh session for "+second.ID) || !h.warned(first.ID) {
		t.Errorf("engage output must tell the driver to start a fresh session, got %q", h.lines)
	}
}

// (b) a session whose rows cover only the same story → no warning.
func TestEngageDoesNotWarnForTheSameStory(t *testing.T) {
	h := newEngageHarness(t, "sess-fresh-2")
	only := h.create("only story")
	h.set(only.ID, "in_progress")
	h.set(only.ID, "blocked")
	h.lines = nil
	h.set(only.ID, "in_progress")

	if adv := h.advisories(only.ID); len(adv) != 0 {
		t.Errorf("same-story re-engage must not warn: %+v", adv)
	}
	if h.warned("fresh session") {
		t.Errorf("unexpected fresh-session output: %q", h.lines)
	}
}

// (c) a repo-configured context budget, exceeded, fires the context-budget
// trigger on its own.
func TestEngageWarnsWhenContextBudgetReached(t *testing.T) {
	h := newEngageHarness(t, "sess-fresh-3")
	SetAgentBudgets(func(string) config.Budget { return config.Budget{Context: 100} })
	only := h.create("budgeted story")
	h.set(only.ID, "in_progress")
	h.set(only.ID, "blocked")
	h.lines = nil
	h.set(only.ID, "in_progress")

	adv := h.advisories(only.ID)
	if len(adv) != 1 || adv[0].Trigger != SessionTriggerContextBudget {
		t.Fatalf("advisories = %+v, want one context-budget", adv)
	}
	if adv[0].ContextTokens != 160 || adv[0].ContextBudget != 100 {
		t.Errorf("context = %d / budget %d, want 160 / 100", adv[0].ContextTokens, adv[0].ContextBudget)
	}
	if !h.warned("context_budget") {
		t.Errorf("warning must name the budget: %q", h.lines)
	}
}

// A budget the session has not reached fires nothing.
func TestEngageContextBudgetNotReached(t *testing.T) {
	h := newEngageHarness(t, "sess-fresh-4")
	SetAgentBudgets(func(string) config.Budget { return config.Budget{Context: 1_000_000} })
	only := h.create("roomy story")
	h.set(only.ID, "in_progress")
	h.set(only.ID, "blocked")
	h.set(only.ID, "in_progress")
	if adv := h.advisories(only.ID); len(adv) != 0 {
		t.Errorf("under budget must not warn: %+v", adv)
	}
}

// (d) no resolved session id → a no-op.
func TestEngageFreshSessionNoSessionIsNoop(t *testing.T) {
	// A session may also be published on this pid or an ancestor; point the
	// publish directory at an empty temp home so none resolves.
	t.Setenv(config.SessionEnv, "")
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	h := newEngageHarness(t, "")
	first := h.create("first")
	h.set(first.ID, "in_progress")
	h.set(first.ID, "blocked")
	second := h.create("second")
	h.set(second.ID, "in_progress")
	if adv := h.advisories(second.ID); len(adv) != 0 {
		t.Errorf("no session: no advisory expected, got %+v", adv)
	}
}
