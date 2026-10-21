package cli

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	"github.com/bobmcallan/satelle/internal/board"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
)

// boardRun runs one command, requires success and returns its stdout.
func boardRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runRoot(t, args...)
	if err != nil {
		t.Fatalf("satelle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func boardPost(t *testing.T, args ...string) verb.AgentMessage {
	t.Helper()
	var m verb.AgentMessage
	if err := json.Unmarshal([]byte(boardRun(t, append([]string{"story", "message"}, args...)...)), &m); err != nil {
		t.Fatalf("parse message: %v", err)
	}
	return m
}

func boardRows(t *testing.T, args ...string) []verb.BoardRow {
	t.Helper()
	var rows []verb.BoardRow
	if err := json.Unmarshal([]byte(boardRun(t, args...)), &rows); err != nil {
		t.Fatalf("parse board: %v", err)
	}
	return rows
}

func boardIDs(rows []verb.BoardRow) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// TestBoardCLIRoundTrip drives the whole surface through the real commands
// (sty_73c9aa10 AC2, AC4, AC7) — and under a hosted dialer that fails the test
// if anything dials out, with no satelle.dev configured (AC9).
func TestBoardCLIRoundTrip(t *testing.T) {
	tempRepo(t)
	t.Setenv(config.SessionEnv, "sess-sender")
	hosted.SetTestGRPCDial(func(context.Context, string) (*grpc.ClientConn, error) {
		t.Error("the message board dialled the hosted server")
		return nil, context.Canceled
	})
	t.Cleanup(func() { hosted.SetTestGRPCDial(nil) })

	one := createStory(t, "board story one")
	two := createStory(t, "board story two")

	// A bare --to keeps meaning a role; the sender is recorded.
	role := boardPost(t, one, "--from", "orchestrator", "--to", "planner", "--body", "role message")
	if role.To != "planner" || role.FromSessionID != "sess-sender" || role.FromSessionName == "" {
		t.Errorf("role message = %+v", role)
	}
	if !strings.HasPrefix(role.FromLocation, "loc_") {
		t.Errorf("location %q is not a loc_ id", role.FromLocation)
	}
	if !regexp.MustCompile(`^msg_[0-9a-f]{8}$`).MatchString(role.ID) {
		t.Errorf("id %q", role.ID)
	}
	sess := boardPost(t, one, "--from", "human", "--to", "session:peer", "--body", "to a session", "--deadline", "1ms")
	story := boardPost(t, two, "--from", "human", "--to", "story", "--body", "to the work")
	prefixed := boardPost(t, two, "--from", "human", "--to", "role:reviewer", "--body", "prefixed role")
	if sess.To != "session:peer" || story.To != "story:"+two || prefixed.To != "reviewer" {
		t.Errorf("addressees: %q %q %q", sess.To, story.To, prefixed.To)
	}
	if sess.Deadline == nil || sess.Deadline.Location() != time.UTC {
		t.Errorf("deadline = %v, want absolute UTC", sess.Deadline)
	}

	// A body over 64 KiB is refused, on the command.
	if out, err := runRoot(t, "story", "message", one, "--from", "human", "--body", strings.Repeat("x", board.MaxBody+1)); err == nil {
		t.Errorf("an oversize body was accepted: %.80s", out)
	}

	// A reply answers the original.
	reply := boardPost(t, one, "--from", "planner", "--to", "orchestrator", "--body", "done", "--reply-to", role.ID)
	if reply.InReplyTo != role.ID {
		t.Errorf("reply links to %q", reply.InReplyTo)
	}

	// Explicit marks, and what the CLI refuses.
	boardRun(t, "story", "message-mark", two, story.ID, "--state", "delivered")
	boardRun(t, "story", "message-mark", two, prefixed.ID, "--state", "answered", "--answer", story.ID)
	boardRun(t, "story", "message-mark", one, sess.ID, "--state", "undeliverable")
	for name, args := range map[string][]string{
		"an unknown message": {"story", "message-mark", one, "msg_00000000", "--state", "delivered"},
		"an invalid state":   {"story", "message-mark", one, role.ID, "--state", "read"},
		"no state":           {"story", "message-mark", one, role.ID},
		"a bad reply-to":     {"story", "message", one, "--from", "human", "--body", "x", "--reply-to", "msg_00000000"},
	} {
		if out, err := runRoot(t, args...); err == nil {
			t.Errorf("%s was accepted: %.80s", name, out)
		}
	}

	// story messages: sender, addressee, state, deadline, overdue.
	var listed []verb.AgentMessage
	if err := json.Unmarshal([]byte(boardRun(t, "story", "messages", one)), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("story messages = %d, want 3", len(listed))
	}
	states := map[string]string{}
	for _, m := range listed {
		states[m.ID] = m.State
		if m.FromSessionName == "" {
			t.Errorf("message %s lists no sender name", m.ID)
		}
	}
	if states[role.ID] != "answered" || states[sess.ID] != "undeliverable" || states[reply.ID] != "waiting" {
		t.Errorf("states = %v", states)
	}
	if got := boardRows(t, "board", "--story", one, "--overdue"); len(got) != 1 || got[0].ID != sess.ID || !got[0].Overdue {
		t.Errorf("board --overdue = %v", boardIDs(got))
	}

	// board spans every story, with each filter.
	all := boardRows(t, "board")
	if len(all) != 5 {
		t.Fatalf("board = %v, want the 5 messages across both stories", boardIDs(all))
	}
	stories := map[string]bool{}
	for _, r := range all {
		stories[r.StoryID] = true
	}
	if !stories[one] || !stories[two] {
		t.Errorf("board covers %v, want both stories", stories)
	}
	if got := boardRows(t, "board", "--state", "waiting"); len(got) != 1 || got[0].ID != reply.ID {
		t.Errorf("board --state waiting = %v", boardIDs(got))
	}
	if got := boardRows(t, "board", "--state", "undeliverable"); len(got) != 1 || got[0].ID != sess.ID {
		t.Errorf("board --state undeliverable = %v", boardIDs(got))
	}
	if got := boardRows(t, "board", "--story", two); len(got) != 2 {
		t.Errorf("board --story = %v, want 2", boardIDs(got))
	}
	if got := boardRows(t, "board", "--session", "peer"); len(got) != 1 || got[0].ID != sess.ID {
		t.Errorf("board --session peer = %v", boardIDs(got))
	}
	if got := boardRows(t, "board", "--session", "sess-sender"); len(got) != 5 {
		t.Errorf("board --session sess-sender = %v, want every message it sent", boardIDs(got))
	}
	if out, err := runRoot(t, "board", "--state", "late"); err == nil {
		t.Errorf("a bad --state was accepted: %.80s", out)
	}
}

// TestReworkEvidenceFingerprintIgnoresMessageMarks (sty_73c9aa10 AC6): marks
// appended after the evidence was fingerprinted — a dispatch marking relays
// delivered, an explicit answered — do not read as progress.
func TestReworkEvidenceFingerprintIgnoresMessageMarks(t *testing.T) {
	tempRepo(t)
	db, err := store.Open(runtimeDBPath(t))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	const id = "sty_fpmark"
	add := func(kind string, payload any, refs json.RawMessage) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		if _, err := db.Ledger.Append(ctx, ledger.AppendInput{StoryID: id, Kind: kind, Actor: "t", Body: kind, Payload: raw, Refs: refs}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	fp := func() string {
		t.Helper()
		got, err := reworkEvidenceFingerprint(ctx, db.Ledger, id)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	add(ledger.KindTelemetryEvent, map[string]any{"kind": "plan-consumed"}, nil)
	add(ledger.KindAgentMessage, map[string]any{"msg_id": "msg_aaaaaaaa", "from": "orchestrator", "to": "planner", "body": "relay"}, nil)
	before := fp()
	add(ledger.KindMessageMark, board.MarkPayload{State: board.Delivered}, board.MarkRefs("msg_aaaaaaaa"))
	add(ledger.KindMessageMark, board.MarkPayload{State: board.Answered, AnswerID: "msg_bbbbbbbb"}, board.MarkRefs("msg_aaaaaaaa"))
	if after := fp(); after != before {
		t.Fatal("message marks changed the rework evidence fingerprint")
	}
	add(ledger.KindTelemetryEvent, map[string]any{"kind": "ac-evidence"}, nil)
	if after := fp(); after == before {
		t.Fatal("control: real evidence should change the fingerprint")
	}
}

// TestMessagesResolverCarriesBoardState: the resolver the dispatch engine reads
// hands it each message's folded state, so only a waiting one is marked.
func TestMessagesResolverCarriesBoardState(t *testing.T) {
	tempRepo(t)
	id := createStory(t, "resolver story")
	m := boardPost(t, id, "--from", "orchestrator", "--to", "planner", "--body", "for the planner")
	boardRun(t, "story", "message-mark", id, m.ID, "--state", "delivered")
	fresh := boardPost(t, id, "--from", "orchestrator", "--to", "planner", "--body", "still waiting")
	// The verbs read the app a store-backed command opened, so the resolver runs
	// inside a probe command that opens one.
	probe := &cobra.Command{
		Use:         "zz-resolver-probe",
		Annotations: needsStore(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			got := messagesResolver()(cmd.Context(), id, []string{"planner"})
			if len(got) != 2 {
				t.Errorf("resolver returned %d messages, want 2", len(got))
			}
			for _, g := range got {
				want := "waiting"
				if g.ID == m.ID {
					want = "delivered"
				}
				if g.ID != m.ID && g.ID != fresh.ID || g.State != want {
					t.Errorf("message %s state = %q, want %q", g.ID, g.State, want)
				}
			}
			return nil
		},
	}
	root := NewRootCmd()
	resetFlagState(root)
	root.AddCommand(probe)
	root.SetArgs([]string{"zz-resolver-probe"})
	if err := executeRoot(root); err != nil {
		t.Fatal(err)
	}
}
