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
