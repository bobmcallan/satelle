package web

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/board"
	"github.com/bobmcallan/satelle/internal/mirror"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// TestMirrorStoryPageShowsMessages (sty_73c9aa10 AC8): the story page's Messages
// panel lists each message's sender, addressee and state, marks an overdue one,
// folds a later mark over the earlier state, and leaves the relay's own
// transcript out. Overdue is computed from the deadline when the page renders.
func TestMirrorStoryPageShowsMessages(t *testing.T) {
	s, err := mirror.Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	rk, slug, id := "rk-msgs", "msgs", "sty_msgs"
	if _, err := s.TouchPartition(ctx, rk, slug, now); err != nil {
		t.Fatal(err)
	}
	story := workitem.Item{ID: id, Kind: workitem.KindStory, Title: "Messages", Status: workitem.StatusInProgress, UpdatedAt: now, CreatedAt: now}
	sb, _ := json.Marshal(story)
	ident, _ := json.Marshal(mirror.IdentityMeta{ProjectName: slug, RepoRoot: "/tmp/" + slug})

	msg := func(evtID, msgID, from, to, body string, at time.Time, extra map[string]any) mirror.ItemRow {
		payload := map[string]any{"msg_id": msgID, "from": from, "to": to, "body": body}
		for k, v := range extra {
			payload[k] = v
		}
		b, _ := json.Marshal(map[string]any{"id": evtID, "story_id": id, "kind": "agent_message", "body": body, "created_at": at, "payload": payload})
		return mirror.ItemRow{ID: evtID, Payload: string(b)}
	}
	mark := func(evtID, msgID string, state board.State, at time.Time) mirror.ItemRow {
		b, _ := json.Marshal(map[string]any{
			"id": evtID, "story_id": id, "kind": "message_mark", "created_at": at,
			"payload": board.MarkPayload{State: state}, "refs": map[string]string{"message": msgID},
		})
		return mirror.ItemRow{ID: evtID, Payload: string(b)}
	}
	past := now.Add(-time.Hour)
	rows := []mirror.ItemRow{
		msg("evt_m1", "msg_00000001", "human", "session:abc", "waiting-and-late", now.Add(-3*time.Hour), map[string]any{
			"from_session_name": "box · repo · sty_x", "deadline": past,
		}),
		msg("evt_m2", "msg_00000002", "orchestrator", "planner", "delivered-then-answered", now.Add(-2*time.Hour), nil),
		mark("evt_k1", "msg_00000002", board.Delivered, now.Add(-90*time.Minute)),
		mark("evt_k2", "msg_00000002", board.Answered, now.Add(-80*time.Minute)),
		msg("evt_m3", "msg_00000003", "human", "story:"+id, "answered-though-late", now.Add(-4*time.Hour), map[string]any{"deadline": past}),
		mark("evt_k3", "msg_00000003", board.Answered, now.Add(-30*time.Minute)),
		msg("evt_m4", "msg_00000004", "consult", "coder", "relay transcript", now.Add(-10*time.Minute), map[string]any{"cc": "*"}),
	}
	for _, r := range []struct {
		kind string
		rows []mirror.ItemRow
	}{
		{"story", []mirror.ItemRow{{ID: id, Payload: string(sb)}}},
		{"ledger_event", rows},
		{"identity", []mirror.ItemRow{{ID: "meta", Payload: string(ident)}}},
	} {
		if err := s.ReplaceKind(ctx, rk, r.kind, r.rows, now); err != nil {
			t.Fatal(err)
		}
	}

	d, _, err := mirrorLoadDetail(ctx, s, rk, "story", id)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Messages) != 3 {
		t.Fatalf("Messages = %+v, want 3 (the relay transcript row is not a message)", d.Messages)
	}
	byID := map[string]messageVM{}
	for _, m := range d.Messages {
		byID[m.ID] = m
	}
	if m := byID["msg_00000001"]; m.State != "waiting" || !m.Overdue || m.To != "session:abc" || !strings.Contains(m.From, "box · repo · sty_x") {
		t.Errorf("late waiting message = %+v", m)
	}
	if m := byID["msg_00000002"]; m.State != "answered" || m.Overdue || m.From != "orchestrator" || m.To != "planner" {
		t.Errorf("folded message = %+v", m)
	}
	if m := byID["msg_00000003"]; m.State != "answered" || m.Overdue {
		t.Errorf("an answered message is never overdue: %+v", m)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", d); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"<h4>Messages</h4>", "msg_00000001", "waiting-and-late", "box · repo · sty_x", "session:abc",
		`chip chip-msg-waiting`, `chip chip-msg-answered`, `chip chip-msg-overdue`, "story:" + id,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("story page lacks %q", want)
		}
	}
	if strings.Count(out, `chip-msg-overdue">overdue`) != 1 {
		t.Errorf("want exactly one overdue marker:\n%s", out)
	}
	if strings.Contains(out, "relay transcript</td>") {
		t.Error("the relay transcript row is listed as a message")
	}

	// A story with no messages shows no panel at all.
	d.Messages = nil
	buf.Reset()
	if err := tmpl.ExecuteTemplate(&buf, "itemDetail", d); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<h4>Messages</h4>") {
		t.Error("an empty Messages panel is rendered")
	}
}
