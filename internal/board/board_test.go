package board_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/board"
	"github.com/bobmcallan/satelle/internal/ledger"
)

var t0 = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func msgRow(id string, at time.Time, payload map[string]any) ledger.Entry {
	if payload == nil {
		payload = map[string]any{}
	}
	if _, ok := payload["msg_id"]; !ok {
		payload["msg_id"] = id
	}
	b, _ := json.Marshal(payload)
	return ledger.Entry{ID: "evt_" + id[4:], StoryID: "sty_1", Kind: ledger.KindAgentMessage, Payload: b, CreatedAt: at}
}

func markRow(msg string, at time.Time, state board.State, answer string) ledger.Entry {
	b, _ := json.Marshal(board.MarkPayload{State: state, AnswerID: answer})
	return ledger.Entry{ID: "evt_m" + at.Format("0405"), StoryID: "sty_1", Kind: ledger.KindMessageMark, Payload: b, Refs: board.MarkRefs(msg), CreatedAt: at}
}

func only(t *testing.T, msgs []board.Message) board.Message {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("folded %d messages, want 1", len(msgs))
	}
	return msgs[0]
}

func TestFoldStateIsTheLatestMark(t *testing.T) {
	m := msgRow("msg_aaaaaaaa", t0, map[string]any{"from": "orchestrator", "to": "planner", "body": "hi"})
	cases := []struct {
		name  string
		marks []ledger.Entry
		want  board.State
	}{
		{"no mark is waiting", nil, board.Waiting},
		{"delivered", []ledger.Entry{markRow("msg_aaaaaaaa", t0.Add(time.Minute), board.Delivered, "")}, board.Delivered},
		{"delivered then answered", []ledger.Entry{
			markRow("msg_aaaaaaaa", t0.Add(time.Minute), board.Delivered, ""),
			markRow("msg_aaaaaaaa", t0.Add(2*time.Minute), board.Answered, "msg_bbbbbbbb"),
		}, board.Answered},
		{"answered then undeliverable: the latest wins", []ledger.Entry{
			markRow("msg_aaaaaaaa", t0.Add(time.Minute), board.Answered, "msg_bbbbbbbb"),
			markRow("msg_aaaaaaaa", t0.Add(2*time.Minute), board.Undeliverable, ""),
		}, board.Undeliverable},
		{"marks out of ledger order are folded by time", []ledger.Entry{
			markRow("msg_aaaaaaaa", t0.Add(2*time.Minute), board.Undeliverable, ""),
			markRow("msg_aaaaaaaa", t0.Add(time.Minute), board.Delivered, ""),
		}, board.Undeliverable},
		{"a mark for another message changes nothing", []ledger.Entry{markRow("msg_zzzzzzzz", t0.Add(time.Minute), board.Answered, "")}, board.Waiting},
		{"an unknown state changes nothing", []ledger.Entry{markRow("msg_aaaaaaaa", t0.Add(time.Minute), board.State("bogus"), "")}, board.Waiting},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := only(t, board.Fold([]ledger.Entry{m}, c.marks))
			if got.State != c.want {
				t.Errorf("state = %s, want %s", got.State, c.want)
			}
		})
	}
}

func TestFoldUpdatedAtAndAnswerFollowTheLatestMark(t *testing.T) {
	m := msgRow("msg_aaaaaaaa", t0, nil)
	got := only(t, board.Fold([]ledger.Entry{m}, nil))
	if !got.UpdatedAt.Equal(t0) {
		t.Errorf("an unmarked message's updated_at = %v, want created_at", got.UpdatedAt)
	}
	at := t0.Add(time.Hour)
	got = only(t, board.Fold([]ledger.Entry{m}, []ledger.Entry{markRow("msg_aaaaaaaa", at, board.Answered, "msg_bbbbbbbb")}))
	if !got.UpdatedAt.Equal(at) || got.AnswerID != "msg_bbbbbbbb" {
		t.Errorf("updated_at = %v answer = %q, want the mark's", got.UpdatedAt, got.AnswerID)
	}
}

func TestOverdueIsDerivedFromDeadlineAndState(t *testing.T) {
	past, future := t0.Add(-time.Hour), t0.Add(time.Hour)
	cases := []struct {
		name     string
		deadline *time.Time
		state    board.State
		want     bool
	}{
		{"past deadline, waiting", &past, board.Waiting, true},
		{"past deadline, delivered", &past, board.Delivered, true},
		{"past deadline, undeliverable", &past, board.Undeliverable, true},
		{"past deadline, answered", &past, board.Answered, false},
		{"future deadline", &future, board.Waiting, false},
		{"no deadline is never overdue", nil, board.Waiting, false},
	}
	for _, c := range cases {
		m := board.Message{State: c.state, Envelope: board.Envelope{Deadline: c.deadline}}
		if got := board.Overdue(m, t0); got != c.want {
			t.Errorf("%s: overdue = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOverdueIsNeverStored(t *testing.T) {
	// A mark carries a state and who set it; nothing about overdue is written.
	b, _ := json.Marshal(board.MarkPayload{State: board.Delivered})
	if strings.Contains(strings.ToLower(string(b)), "overdue") {
		t.Errorf("mark payload stores overdue: %s", b)
	}
	if board.State("overdue").Valid() {
		t.Error("overdue must not be a state")
	}
}

func TestFoldDecodesTheEnvelopeAndLegacyRows(t *testing.T) {
	dl := t0.Add(30 * time.Minute)
	m := msgRow("msg_aaaaaaaa", t0, map[string]any{
		"from": "human", "to": "session:abc", "body": "look", "from_session_id": "s1",
		"from_session_name": "host · repo", "from_location": "loc_1", "in_reply_to": "msg_00000000", "deadline": dl,
	})
	got := only(t, board.Fold([]ledger.Entry{m}, nil))
	if got.Kind != board.KindSession || got.Value != "abc" || got.FromSessionID != "s1" || got.FromSessionName != "host · repo" ||
		got.FromLocation != "loc_1" || got.InReplyTo != "msg_00000000" || got.Deadline == nil || !got.Deadline.Equal(dl) {
		t.Errorf("decoded %+v", got)
	}
	// A row written before the board existed has no msg_id: its ledger id is its id.
	legacy := ledger.Entry{ID: "evt_deadbeef", StoryID: "sty_1", Kind: ledger.KindAgentMessage,
		Payload: json.RawMessage(`{"from":"orchestrator","to":"planner","body":"old"}`), CreatedAt: t0}
	got = only(t, board.Fold([]ledger.Entry{legacy}, []ledger.Entry{markRow("evt_deadbeef", t0.Add(time.Minute), board.Delivered, "")}))
	if got.ID != "evt_deadbeef" || got.Kind != board.KindRole || got.Value != "planner" || got.State != board.Delivered {
		t.Errorf("legacy row = %+v", got)
	}
}

func TestAddress(t *testing.T) {
	cases := []struct {
		arg, to string
		kind    board.Kind
		value   string
		bad     bool
	}{
		{"", "*", board.KindAll, "", false},
		{"*", "*", board.KindAll, "", false},
		{"story", "story:sty_1", board.KindStory, "sty_1", false},
		{"role:planner", "planner", board.KindRole, "planner", false},
		{"planner", "planner", board.KindRole, "planner", false}, // a bare role keeps today's meaning
		{"session:abc", "session:abc", board.KindSession, "abc", false},
		{"role:", "", "", "", true},
		{"session:", "", "", "", true},
	}
	for _, c := range cases {
		to, err := board.Address(c.arg, "sty_1")
		if c.bad {
			if err == nil {
				t.Errorf("Address(%q) accepted", c.arg)
			}
			continue
		}
		if err != nil || to != c.to {
			t.Errorf("Address(%q) = %q, %v; want %q", c.arg, to, err, c.to)
			continue
		}
		if k, v := board.Addressee(to); k != c.kind || v != c.value {
			t.Errorf("Addressee(%q) = %s,%q; want %s,%q", to, k, v, c.kind, c.value)
		}
	}
}

func TestNewIDShape(t *testing.T) {
	re := regexp.MustCompile(`^msg_[0-9a-f]{8}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := board.NewID()
		if !re.MatchString(id) {
			t.Fatalf("id %q is not msg_<8hex>", id)
		}
		seen[id] = true
	}
	if len(seen) < 49 {
		t.Errorf("ids collide: %d distinct of 50", len(seen))
	}
}

func TestParseDeadline(t *testing.T) {
	got, err := board.ParseDeadline("30m", t0)
	if err != nil || !got.Equal(t0.Add(30*time.Minute)) || got.Location() != time.UTC {
		t.Errorf("30m = %v, %v", got, err)
	}
	got, err = board.ParseDeadline("2026-10-22T10:00:00+10:00", t0)
	if err != nil || !got.Equal(time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)) || got.Location() != time.UTC {
		t.Errorf("RFC3339 = %v, %v", got, err)
	}
	if got, err = board.ParseDeadline("", t0); err != nil || !got.IsZero() {
		t.Errorf("empty = %v, %v", got, err)
	}
	for _, bad := range []string{"soon", "-5m", "0s"} {
		if _, err := board.ParseDeadline(bad, t0); err == nil {
			t.Errorf("deadline %q accepted", bad)
		}
	}
}

func TestTranscriptRowsAreNotMessages(t *testing.T) {
	relay := msgRow("msg_cccccccc", t0, map[string]any{"from": "consult", "to": "coder", "cc": "*"})
	if !board.Transcript(relay) {
		t.Error("a cc=* relay row must be a transcript")
	}
	if board.Transcript(msgRow("msg_dddddddd", t0, map[string]any{"to": "coder"})) {
		t.Error("a directed row is not a transcript")
	}
}
