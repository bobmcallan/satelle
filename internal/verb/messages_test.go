package verb_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func TestMessageVerbsRegistered(t *testing.T) {
	cat := verb.Catalog()
	want := map[string]bool{"story-message": false, "story-messages": false}
	for _, n := range cat {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, ok := range want {
		if !ok {
			t.Errorf("Catalog missing %s", n)
		}
	}
}

func TestStoryMessageAppendsLedgerRow(t *testing.T) {
	wire(t)
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": "msg"}), &it); err != nil {
		t.Fatal(err)
	}
	raw := call(t, "story-message", map[string]any{
		"id": it.ID, "from": "orchestrator", "to": "executor", "body": "do the slice",
	})
	var got verb.AgentMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.From != "orchestrator" || got.To != "executor" || got.Body != "do the slice" {
		t.Fatalf("row fields: %+v", got)
	}
	if got.ID == "" || got.CreatedAt.IsZero() {
		t.Fatalf("missing id/created_at: %+v", got)
	}
	if got.EngagementSHA != "" {
		t.Errorf("unengaged story should store empty engagement_sha, got %q", got.EngagementSHA)
	}
	listed := call(t, "ledger-list", map[string]any{"story_id": it.ID, "kind": ledger.KindAgentMessage})
	var entries []ledger.Entry
	if err := json.Unmarshal(listed, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("ledger rows = %d, want 1", len(entries))
	}
}

func TestStoryMessagesOrderingAndFilters(t *testing.T) {
	wire(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "filter"}), &it)

	call(t, "story-message", map[string]any{"id": it.ID, "from": "human", "to": "executor", "body": "one"})
	time.Sleep(2 * time.Millisecond)
	mid := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "body": "two-star"}) // to default *
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "human", "to": "reviewer", "body": "three"})

	raw := call(t, "story-messages", map[string]any{"id": it.ID})
	var all []verb.AgentMessage
	json.Unmarshal(raw, &all)
	if len(all) != 3 {
		t.Fatalf("all = %d, want 3: %+v", len(all), all)
	}
	if all[0].Body != "one" || all[1].Body != "two-star" || all[2].Body != "three" {
		t.Fatalf("order: %v %v %v", all[0].Body, all[1].Body, all[2].Body)
	}
	if all[1].To != "*" {
		t.Errorf("default to = %q, want *", all[1].To)
	}

	raw = call(t, "story-messages", map[string]any{"id": it.ID, "to": "executor"})
	var exec []verb.AgentMessage
	json.Unmarshal(raw, &exec)
	if len(exec) != 2 {
		t.Fatalf("to=executor got %d, want 2 (executor + *)", len(exec))
	}

	raw = call(t, "story-messages", map[string]any{"id": it.ID, "since": mid.Format(time.RFC3339Nano)})
	var later []verb.AgentMessage
	json.Unmarshal(raw, &later)
	if len(later) != 2 {
		t.Fatalf("since mid got %d, want 2", len(later))
	}
}

func TestMessagesSinceExcludes(t *testing.T) {
	wire(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "window"}), &it)

	call(t, "story-message", map[string]any{"id": it.ID, "from": "human", "to": "executor", "body": "before"})
	time.Sleep(2 * time.Millisecond)
	payload, _ := json.Marshal(map[string]any{"head_sha": "abc123", "dirty": false, "to": "in_progress"})
	call(t, "ledger-append", map[string]any{
		"story_id": it.ID, "kind": ledger.KindEngagementBaseline, "payload": json.RawMessage(payload),
	})
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "reviewer", "body": "for-reviewer"})
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "executor", "body": "for-executor"})
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "body": "for-all"})

	got := verb.MessagesSince(context.Background(), it.ID, []string{"executor"})
	bodies := make([]string, len(got))
	for i, m := range got {
		bodies[i] = m.Body
	}
	joined := strings.Join(bodies, ",")
	if strings.Contains(joined, "before") {
		t.Errorf("pre-engagement message leaked: %s", joined)
	}
	if strings.Contains(joined, "for-reviewer") {
		t.Errorf("wrong recipient leaked: %s", joined)
	}
	if !strings.Contains(joined, "for-executor") || !strings.Contains(joined, "for-all") {
		t.Errorf("missing post-engagement executor/*: %s", joined)
	}
}

func TestMessagesSinceAfterReanchor(t *testing.T) {
	wire(t)
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "reanchor"}), &it)

	first, _ := json.Marshal(map[string]any{"head_sha": "abc123", "dirty": false, "to": "in_progress"})
	call(t, "ledger-append", map[string]any{
		"story_id": it.ID, "kind": ledger.KindEngagementBaseline, "payload": json.RawMessage(first),
	})
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "executor", "body": "during-first"})
	time.Sleep(2 * time.Millisecond)
	re, _ := json.Marshal(map[string]any{
		"from": "blocked", "to": "in_progress", "head_sha": "def456", "files": []string{},
		"reanchor_resume": true,
	})
	call(t, "ledger-append", map[string]any{
		"story_id": it.ID, "kind": ledger.KindChangeRecord, "payload": json.RawMessage(re),
	})
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "executor", "body": "after-resume"})

	got := verb.MessagesSince(context.Background(), it.ID, []string{"executor"})
	bodies := make([]string, len(got))
	for i, m := range got {
		bodies[i] = m.Body
		if m.Body == "after-resume" && m.EngagementSHA != "def456" {
			t.Errorf("after-resume stamped sha %q, want def456", m.EngagementSHA)
		}
	}
	joined := strings.Join(bodies, ",")
	if strings.Contains(joined, "during-first") {
		t.Errorf("pre-reanchor message leaked: %s", joined)
	}
	if !strings.Contains(joined, "after-resume") {
		t.Errorf("post-reanchor message missing: %s", joined)
	}
}

func TestAgentDispatchDocMentionsMessages(t *testing.T) {
	p := filepath.Join("..", "help", "topics", "agent-dispatch.md")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"messages[]", "story messages", "story message", "agent_message"} {
		if !strings.Contains(s, want) {
			t.Errorf("agent-dispatch.md missing %q", want)
		}
	}
}

// bodiesOf lists the bodies MessagesSince returns for addr, oldest first.
func bodiesOf(t *testing.T, id, addr string) []string {
	t.Helper()
	var out []string
	for _, m := range verb.MessagesSince(context.Background(), id, []string{addr}) {
		out = append(out, m.Body)
	}
	return out
}

// sty_36ac4319 AC1: a message sent while the story is still at the start state
// reaches the performer that proposes from there, and is stamped with no SHA.
func TestMessageReachesProposingPerformerFromBacklog(t *testing.T) {
	withWiring(t)
	newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	var seen []verb.AgentMessage
	verb.SetExecutorDispatcher(dispatcherFunc(func(ctx context.Context, it workitem.Item, to string) (verb.DispatchResult, error) {
		if to != "plan" {
			return verb.DispatchResult{}, nil
		}
		seen = verb.MessagesSince(ctx, it.ID, []string{"planner"})
		call(t, "story-doc-attach", map[string]any{"story_id": it.ID, "name": "plan", "type": "plan", "body": "the plan"})
		return verb.DispatchResult{Dispatched: true, Agent: "planner", Skill: "plan"}, nil
	}))
	it := newFeature(t)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "planner", "body": "decision: keep AC2"})
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "reviewer", "body": "not for the planner"})

	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})

	if len(seen) != 1 || seen[0].Body != "decision: keep AC2" {
		t.Fatalf("the proposing planner saw %+v, want exactly the backlog message addressed to it", seen)
	}
	if seen[0].EngagementSHA != "" {
		t.Errorf("a pre-engagement message is stamped with no SHA, got %q", seen[0].EngagementSHA)
	}
}

// sty_36ac4319 AC2: a recover back to the start state opens a new window;
// the engagement's messages do not leak into it.
func TestPreEngagementWindowExcludesEarlierEngagement(t *testing.T) {
	newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	it := newFeature(t)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "planner", "body": "backlog-one"})
	time.Sleep(2 * time.Millisecond)

	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "planner", "body": "engagement-A"})
	if got := strings.Join(bodiesOf(t, it.ID, "planner"), ","); got != "engagement-A" {
		t.Fatalf("engaged at plan, planner sees %q, want only engagement-A", got)
	}
	time.Sleep(2 * time.Millisecond)

	call(t, "story-set", map[string]any{"id": it.ID, "status": "backlog"}) // the declared recover edge
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "planner", "body": "engagement-B"})

	if got := strings.Join(bodiesOf(t, it.ID, "planner"), ","); got != "engagement-B" {
		t.Errorf("back at the start state the planner sees %q, want only engagement-B", got)
	}
	msgs := verb.MessagesSince(context.Background(), it.ID, []string{"planner"})
	if len(msgs) == 1 && msgs[0].EngagementSHA != "" {
		t.Errorf("the message stamp and the read window must agree: stamped %q in the pre-engagement window", msgs[0].EngagementSHA)
	}
}

// sty_36ac4319 AC2: a park from the start state and its resume re-anchor (written
// with the resume transition) do not close the pre-engagement window; the
// message sent before the park stays excluded.
func TestPreEngagementWindowAfterParkFromStart(t *testing.T) {
	withWiring(t)
	newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	verb.SetExecutorDispatcher(dispatcherFunc(func(_ context.Context, _ workitem.Item, to string) (verb.DispatchResult, error) {
		if to != "plan" {
			return verb.DispatchResult{}, nil
		}
		return verb.DispatchResult{Agent: "planner"}, &verb.PerformerReject{Notes: "premise wrong"}
	}))
	it := newFeature(t)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "planner", "body": "before-park"})
	time.Sleep(2 * time.Millisecond)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	if got := statusOf(t, it.ID).Status; got != "blocked" {
		t.Fatalf("status = %q, want blocked", got)
	}
	call(t, "story-set", map[string]any{"id": it.ID, "status": "backlog"})
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "planner", "body": "after-resume"})

	if got := strings.Join(bodiesOf(t, it.ID, "planner"), ","); got != "after-resume" {
		t.Errorf("planner sees %q, want only after-resume", got)
	}
}

// sty_36ac4319 AC2 regression: a story that is engaged (never at its start
// state) keeps its SHA-stamped window untouched, and a message from another
// engagement's SHA is still excluded.
func TestEngagedStoryKeepsSHAStampedWindow(t *testing.T) {
	newReadinessRig(t, readinessWF, func(string, int) verb.GateDecision { return acceptAll() })
	it := newFeature(t)
	call(t, "story-set", map[string]any{"id": it.ID, "status": "plan"})
	call(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	time.Sleep(2 * time.Millisecond)
	call(t, "story-message", map[string]any{"id": it.ID, "from": "orchestrator", "to": "executor", "body": "current"})

	msgs := verb.MessagesSince(context.Background(), it.ID, []string{"executor"})
	if len(msgs) != 1 || msgs[0].Body != "current" {
		t.Fatalf("in_progress executor sees %+v, want the current message", msgs)
	}
	sha := msgs[0].EngagementSHA
	if sha == "" {
		t.Skip("no git HEAD available: the engagement baseline carries no SHA to compare")
	}
	other, _ := json.Marshal(verb.AgentMessage{From: "orchestrator", To: "executor", Body: "old-engagement", EngagementSHA: "0000000"})
	call(t, "ledger-append", map[string]any{
		"story_id": it.ID, "kind": ledger.KindAgentMessage, "body": "old-engagement", "payload": json.RawMessage(other),
	})
	if got := strings.Join(bodiesOf(t, it.ID, "executor"), ","); got != "current" {
		t.Errorf("executor sees %q, want a message stamped with another SHA excluded", got)
	}
}
