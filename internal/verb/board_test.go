package verb_test

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/board"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

func newBoardStory(t *testing.T, title string) string {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": title}), &it); err != nil {
		t.Fatal(err)
	}
	return it.ID
}

func post(t *testing.T, req map[string]any) verb.AgentMessage {
	t.Helper()
	var m verb.AgentMessage
	if err := json.Unmarshal(call(t, "story-message", req), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func listMessages(t *testing.T, req map[string]any) []verb.AgentMessage {
	t.Helper()
	var out []verb.AgentMessage
	if err := json.Unmarshal(call(t, "story-messages", req), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func byID(t *testing.T, msgs []verb.AgentMessage, id string) verb.AgentMessage {
	t.Helper()
	for _, m := range msgs {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("message %s not listed", id)
	return verb.AgentMessage{}
}

func TestBoardVerbsRegistered(t *testing.T) {
	have := map[string]bool{}
	for _, n := range verb.Catalog() {
		have[n] = true
	}
	for _, want := range []string{"story-message-mark", "board-list"} {
		if !have[want] {
			t.Errorf("Catalog missing %s", want)
		}
	}
}

func TestStoryMessageRecordsAddresseeAndSender(t *testing.T) {
	wire(t)
	id := newBoardStory(t, "addressing")
	msgID := regexp.MustCompile(`^msg_[0-9a-f]{8}$`)
	cases := []struct{ to, stored string }{
		{"story", "story:" + id},
		{"role:planner", "planner"},
		{"planner", "planner"}, // a bare role keeps today's meaning
		{"session:s-42", "session:s-42"},
		{"*", "*"},
		{"", "*"},
	}
	for _, c := range cases {
		m := post(t, map[string]any{
			"id": id, "from": "human", "to": c.to, "body": "hello " + c.to,
			"from_session_id": "sess-1", "from_session_name": "box · repo", "from_location": "loc_abc",
		})
		if !msgID.MatchString(m.ID) {
			t.Errorf("to %q: id %q is not msg_<8hex>", c.to, m.ID)
		}
		if m.To != c.stored {
			t.Errorf("to %q stored as %q, want %q", c.to, m.To, c.stored)
		}
		if m.FromSessionID != "sess-1" || m.FromSessionName != "box · repo" || m.FromLocation != "loc_abc" {
			t.Errorf("to %q: sender %q/%q/%q", c.to, m.FromSessionID, m.FromSessionName, m.FromLocation)
		}
		if m.State != string(board.Waiting) {
			t.Errorf("to %q: a new message is %q, want waiting", c.to, m.State)
		}
	}
}

func TestStoryMessageRefusesBadAddressAndOversizeBody(t *testing.T) {
	wire(t)
	id := newBoardStory(t, "refusals")
	if _, err := dispatchRaw(t, "story-message", map[string]any{"id": id, "from": "human", "to": "session:", "body": "x"}); err == nil {
		t.Error("an empty session address was accepted")
	}
	atCap := strings.Repeat("a", board.MaxBody)
	post(t, map[string]any{"id": id, "from": "human", "body": atCap})
	_, err := dispatchRaw(t, "story-message", map[string]any{"id": id, "from": "human", "body": atCap + "a"})
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Errorf("a body over 64 KiB was accepted: %v", err)
	}
	if n := len(listMessages(t, map[string]any{"id": id})); n != 1 {
		t.Errorf("%d messages recorded, want only the one at the cap", n)
	}
}

func TestStoryMessageDeadlineIsAbsoluteUTC(t *testing.T) {
	wire(t)
	id := newBoardStory(t, "deadline")
	before := time.Now()
	m := post(t, map[string]any{"id": id, "from": "human", "body": "soon", "deadline": "30m"})
	if m.Deadline == nil {
		t.Fatal("no deadline stored")
	}
	if d := m.Deadline.Sub(before); d < 29*time.Minute || d > 31*time.Minute {
		t.Errorf("deadline %v is not ~30m from now", d)
	}
	if m.Deadline.Location() != time.UTC {
		t.Errorf("deadline zone = %v, want UTC", m.Deadline.Location())
	}
	fixed := post(t, map[string]any{"id": id, "from": "human", "body": "dated", "deadline": "2030-01-02T03:04:05+10:00"})
	if want := time.Date(2030, 1, 1, 17, 4, 5, 0, time.UTC); !fixed.Deadline.Equal(want) {
		t.Errorf("RFC3339 deadline = %v, want %v", fixed.Deadline, want)
	}
	if _, err := dispatchRaw(t, "story-message", map[string]any{"id": id, "from": "human", "body": "x", "deadline": "whenever"}); err == nil {
		t.Error("a malformed deadline was accepted")
	}
}

func TestReplyMarksTheOriginalAnswered(t *testing.T) {
	wire(t)
	id := newBoardStory(t, "reply")
	orig := post(t, map[string]any{"id": id, "from": "orchestrator", "to": "planner", "body": "question?"})
	reply := post(t, map[string]any{"id": id, "from": "planner", "to": "orchestrator", "body": "answer", "in_reply_to": orig.ID, "from_session_id": "p1"})
	if reply.InReplyTo != orig.ID {
		t.Errorf("reply links to %q, want %q", reply.InReplyTo, orig.ID)
	}
	all := listMessages(t, map[string]any{"id": id})
	got := byID(t, all, orig.ID)
	if got.State != "answered" || got.AnswerID != reply.ID {
		t.Errorf("original = %s answered by %q; want answered by %s", got.State, got.AnswerID, reply.ID)
	}
	if byID(t, all, reply.ID).State != "waiting" {
		t.Error("the reply itself should still be waiting")
	}
	if _, err := dispatchRaw(t, "story-message", map[string]any{"id": id, "from": "planner", "body": "x", "in_reply_to": "msg_00000000"}); err == nil {
		t.Error("a reply to an unknown message was accepted")
	}
	if n := len(listMessages(t, map[string]any{"id": id})); n != 2 {
		t.Errorf("%d messages after a refused reply, want 2", n)
	}
}

func TestStoryMessageMarkVerb(t *testing.T) {
	wire(t)
	id := newBoardStory(t, "marks")
	a := post(t, map[string]any{"id": id, "from": "human", "to": "session:s9", "body": "a"})
	b := post(t, map[string]any{"id": id, "from": "human", "to": "planner", "body": "b"})

	mark := func(msg, state, answer string) error {
		req := map[string]any{"id": id, "message": msg, "state": state}
		if answer != "" {
			req["answer_id"] = answer
		}
		_, err := dispatchRaw(t, "story-message-mark", req)
		return err
	}
	if err := mark(a.ID, "delivered", ""); err != nil {
		t.Fatal(err)
	}
	if got := byID(t, listMessages(t, map[string]any{"id": id}), a.ID); got.State != "delivered" {
		t.Errorf("state = %s, want delivered", got.State)
	}
	if err := mark(a.ID, "answered", b.ID); err != nil {
		t.Fatal(err)
	}
	if got := byID(t, listMessages(t, map[string]any{"id": id}), a.ID); got.State != "answered" || got.AnswerID != b.ID {
		t.Errorf("state = %s answer = %q, want answered by %s", got.State, got.AnswerID, b.ID)
	}
	// The latest mark wins: a message can fall back to undeliverable.
	if err := mark(a.ID, "undeliverable", ""); err != nil {
		t.Fatal(err)
	}
	if got := listMessages(t, map[string]any{"id": id, "state": "undeliverable"}); len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("--state undeliverable listed %+v", got)
	}

	for name, err := range map[string]error{
		"an unknown message":       mark("msg_00000000", "delivered", ""),
		"an invalid state":         mark(b.ID, "read", ""),
		"waiting (nothing marks)":  mark(b.ID, "waiting", ""),
		"an answer on delivered":   mark(b.ID, "delivered", a.ID),
		"an answer naming nothing": mark(b.ID, "answered", "msg_00000000"),
	} {
		if err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := dispatchRaw(t, "story-messages", map[string]any{"id": id, "state": "overdue"}); err == nil {
		t.Error("overdue is not a state and must not filter as one")
	}
}

func TestOverdueFilterAndFlag(t *testing.T) {
	wire(t)
	id := newBoardStory(t, "overdue")
	soon := post(t, map[string]any{"id": id, "from": "human", "body": "soon", "deadline": "1ms"})
	later := post(t, map[string]any{"id": id, "from": "human", "body": "later", "deadline": "1h"})
	none := post(t, map[string]any{"id": id, "from": "human", "body": "none"})
	// time-subject: let the 1ms deadline pass. A slower clock only widens the gap.
	time.Sleep(20 * time.Millisecond)
	all := listMessages(t, map[string]any{"id": id})
	if !byID(t, all, soon.ID).Overdue || byID(t, all, later.ID).Overdue || byID(t, all, none.ID).Overdue {
		t.Errorf("overdue flags: soon=%v later=%v none=%v", byID(t, all, soon.ID).Overdue, byID(t, all, later.ID).Overdue, byID(t, all, none.ID).Overdue)
	}
	if got := listMessages(t, map[string]any{"id": id, "overdue": true}); len(got) != 1 || got[0].ID != soon.ID {
		t.Errorf("--overdue listed %+v", got)
	}
	call(t, "story-message-mark", map[string]any{"id": id, "message": soon.ID, "state": "answered"})
	if got := listMessages(t, map[string]any{"id": id, "overdue": true}); len(got) != 0 {
		t.Errorf("an answered message is still overdue: %+v", got)
	}
	// Overdue is read-time: no stored row carries it.
	var rows []ledger.Entry
	if err := json.Unmarshal(call(t, "ledger-list", map[string]any{"story_id": id}), &rows); err != nil {
		t.Fatal(err)
	}
	stored := 0
	for _, r := range rows {
		if r.Kind != ledger.KindAgentMessage && r.Kind != ledger.KindMessageMark {
			continue
		}
		stored++
		if strings.Contains(string(r.Payload), "overdue") {
			t.Errorf("ledger row %s stores overdue: %s", r.ID, r.Payload)
		}
	}
	// 3 messages and 1 mark: a vacuous scan would prove nothing.
	if stored != 4 {
		t.Errorf("scanned %d message/mark rows, want 4", stored)
	}
}

func TestBoardListSpansStoriesAndFilters(t *testing.T) {
	wire(t)
	s1, s2 := newBoardStory(t, "one"), newBoardStory(t, "two")
	a := post(t, map[string]any{"id": s1, "from": "human", "to": "session:peer", "body": "a", "from_session_id": "me", "from_session_name": "host · one"})
	b := post(t, map[string]any{"id": s2, "from": "human", "to": "planner", "body": "b", "from_session_id": "other", "deadline": "1ms"})
	post(t, map[string]any{"id": s2, "from": "consult", "to": "coder", "cc": "*", "body": "relay transcript"})
	call(t, "story-message-mark", map[string]any{"id": s1, "message": a.ID, "state": "delivered"})
	// time-subject: let the 1ms deadline pass. A slower clock only widens the gap.
	time.Sleep(20 * time.Millisecond)

	list := func(req map[string]any) []verb.BoardRow {
		t.Helper()
		var out []verb.BoardRow
		if err := json.Unmarshal(call(t, "board-list", req), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	ids := func(rows []verb.BoardRow) string {
		var s []string
		for _, r := range rows {
			s = append(s, r.ID)
		}
		return strings.Join(s, ",")
	}

	all := list(map[string]any{})
	if ids(all) != a.ID+","+b.ID {
		t.Fatalf("board = %s, want %s,%s (both stories, transcript row left out)", ids(all), a.ID, b.ID)
	}
	if all[0].StoryID != s1 || all[1].StoryID != s2 || all[0].FromSessionName != "host · one" || all[0].State != "delivered" {
		t.Errorf("rows: %+v", all)
	}
	if got := ids(list(map[string]any{"state": "waiting"})); got != b.ID {
		t.Errorf("--state waiting = %s, want %s", got, b.ID)
	}
	if got := ids(list(map[string]any{"overdue": true})); got != b.ID {
		t.Errorf("--overdue = %s, want %s", got, b.ID)
	}
	if got := ids(list(map[string]any{"story": s1})); got != a.ID {
		t.Errorf("--story = %s, want %s", got, a.ID)
	}
	if got := ids(list(map[string]any{"session": "me"})); got != a.ID {
		t.Errorf("--session (sender) = %s, want %s", got, a.ID)
	}
	if got := ids(list(map[string]any{"session": "peer"})); got != a.ID {
		t.Errorf("--session (addressee) = %s, want %s", got, a.ID)
	}
	if _, err := dispatchRaw(t, "board-list", map[string]any{"state": "overdue"}); err == nil {
		t.Error("a bad --state was accepted")
	}
}

// TestMessageMarksAreNotEvidence: a message_mark is delivery bookkeeping, so it
// never makes a recorded verdict stale — including the marks a re-presented
// readiness edge's planner dispatch writes on the orchestrator's relays.
func TestMessageMarksAreNotEvidence(t *testing.T) {
	if verb.IsEvidenceRow(ledger.Entry{Kind: ledger.KindMessageMark}) {
		t.Fatal("a message_mark row counted as evidence")
	}
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	accept, _ := json.Marshal(map[string]any{
		"from": "backlog", "to": "plan", "skill": "satelle-story-plan-review", "notes": "ok",
		"reviewed": "QUOTED PLAN TEXT", "recorded_at": at.Format(time.RFC3339Nano),
	})
	mark, _ := json.Marshal(board.MarkPayload{State: board.Delivered})
	relay, _ := json.Marshal(map[string]any{"from": "orchestrator", "to": "planner", "body": "decisions"})
	rows := []ledger.Entry{
		{ID: "evt_1", Kind: ledger.KindReviewAccept, Payload: accept, CreatedAt: at},
		// The re-presentation: the relay (exempt, a message to the performer), then
		// the planner dispatch that delivers it and marks it.
		{ID: "evt_2", Kind: ledger.KindAgentMessage, Payload: relay, CreatedAt: at.Add(time.Minute)},
		{ID: "evt_3", Kind: ledger.KindMessageMark, Payload: mark, Refs: board.MarkRefs("evt_2"), CreatedAt: at.Add(2 * time.Minute)},
		{ID: "evt_4", Kind: ledger.KindMessageMark, Payload: mark, Refs: board.MarkRefs("evt_2"), CreatedAt: at.Add(3 * time.Minute)},
	}
	ex := verb.StaleExemption{DocName: "plan", Performer: "planner"}
	got := verb.PriorVerdictsFrom(rows, "backlog", "plan", ex)
	if len(got) != 1 {
		t.Fatalf("%d verdicts, want 1", len(got))
	}
	if got[0].ReviewedStale || len(got[0].EvidenceSince) != 0 || got[0].Reviewed != "QUOTED PLAN TEXT" {
		t.Errorf("verdict after marks = %+v; want untouched, quotation kept", got[0])
	}
	// Control: a row that IS evidence after the same accept still stales it.
	rows = append(rows, ledger.Entry{ID: "evt_5", Kind: ledger.KindStoryDocAttached, Payload: json.RawMessage(`{"name":"notes"}`), CreatedAt: at.Add(4 * time.Minute)})
	if got = verb.PriorVerdictsFrom(rows, "backlog", "plan", ex); !got[0].ReviewedStale {
		t.Error("control: an attachment after the accept should stale the verdict")
	}
}

// TestMarksDoNotStaleAGatePayload is the same rule end to end: the second
// presentation's prior_verdicts file keeps its quotation after the story
// receives a message and a mark.
func TestMarksDoNotStaleAGatePayload(t *testing.T) {
	const quote = "MARK-E2E-QUOTATION"
	runner, story := newPVHarness(t, []string{
		verdictJSON("reject", "first", quote),
		verdictJSON("reject", "second", quote),
	}, nil)
	present := func() {
		t.Helper()
		if _, err := dispatchRaw(t, "story-set", map[string]any{"id": story.ID, "status": "in_progress"}); err == nil {
			t.Fatal("want the reviewer's reject")
		}
	}
	// A directed message is itself evidence, so it is on the story before the
	// first verdict; only the mark that follows is under test.
	m := post(t, map[string]any{"id": story.ID, "from": "orchestrator", "to": "planner", "body": "relay"})
	present()
	if _, err := verb.StoryMessageMark(context.Background(), verb.MarkRequest{ID: story.ID, Message: m.ID, State: "delivered"}); err != nil {
		t.Fatal(err)
	}
	present()
	if strings.Contains(runner.priorFiles[1], "reviewed_stale") || !strings.Contains(runner.priorFiles[1], quote) {
		t.Fatalf("a mark staled the verdict:\n%s", runner.priorFiles[1])
	}
}
