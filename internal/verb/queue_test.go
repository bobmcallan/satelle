package verb_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/store"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// waitWF puts a wait step between the performing step and the terminal. The
// step and status names are fixture data.
var waitWF = routeHalves(
	`["*"]
obligations = ["raised", "coded", "queued", "closed"]
park = { state = "blocked" }
cancel = { state = "cancelled" }
`,
	`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[queued]
status = "awaiting_release"
wait = true
requires = ["coded"]

[closed]
status = "done"
terminal = true
requires = ["queued"]
`)

// localRepo is a git repository with one commit and no remote, the process
// working in it, and the wait workflow wired.
func localRepo(t *testing.T, limit int) (*store.DB, string) {
	t.Helper()
	dir := gitRepo(t)
	if out := gitOut(t, dir, "remote"); out != "" {
		t.Fatalf("fixture repo must have no remote, got %q", out)
	}
	chdir(t, dir)
	db := wireWithWorkflowsStore(t, waitWF)
	if limit > 0 {
		verb.SetEngagementMode(config.Config{Engagement: config.EngagementConfig{Limit: limit}})
	}
	return db, gitOut(t, dir, "rev-parse", "--show-toplevel")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func queuedStory(t *testing.T, title string) string {
	t.Helper()
	var it workitem.Item
	if err := json.Unmarshal(call(t, "story-create", map[string]any{"title": title, "category": "feature"}), &it); err != nil {
		t.Fatal(err)
	}
	return it.ID
}

func enterStatus(t *testing.T, id, status string) error {
	t.Helper()
	_, err := dispatchRaw(t, "story-set", map[string]any{"id": id, "status": status})
	return err
}

func queueStories(t *testing.T, state string) []string {
	t.Helper()
	var res struct {
		Entries []verb.QueueEntry `json:"entries"`
	}
	if err := json.Unmarshal(call(t, "story-queue", map[string]any{"state": state}), &res); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range res.Entries {
		ids = append(ids, e.Story)
	}
	return ids
}

// AC1, AC2, AC4: under [engagement] limit = 1, entering the wait step stops the
// story performing and frees its tree — a second story engages from the same
// tree, which the limit refused while the first was in progress — and records
// one queue entry naming the story, its branch and its head sha, all in a
// repository with no remote.
func TestWaitStepStopsPerformingAndQueuesTheStory(t *testing.T) {
	db, tree := localRepo(t, 1)
	ctx := context.Background()
	first, second := queuedStory(t, "first"), queuedStory(t, "second")

	if err := enterStatus(t, first, "in_progress"); err != nil {
		t.Fatalf("engage first: %v", err)
	}
	if err := enterStatus(t, second, "in_progress"); err == nil || !strings.Contains(err.Error(), "[engagement] limit = 1") {
		t.Fatalf("a second story is refused by the limit while the first performs: %v", err)
	}
	if got := verb.DirtyTreeOwner(ctx, second)(tree); got != first {
		t.Fatalf("the in-progress story owns the tree: owner = %q, want %q", got, first)
	}

	if err := enterStatus(t, first, "awaiting_release"); err != nil {
		t.Fatalf("enter the wait step: %v", err)
	}
	if isPerforming(t, first) {
		t.Fatal("a story at a wait step is not performing")
	}
	if got := verb.DirtyTreeOwner(ctx, second)(tree); got != "" {
		t.Fatalf("a waiting story no longer owns its tree: owner = %q", got)
	}

	rows, err := db.Ledger.ListByStory(ctx, first, ledger.KindQueueEnqueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want one queue_enqueued event on the story's chain, got %d", len(rows))
	}
	var p ledger.QueueEnqueuedPayload
	if err := json.Unmarshal(rows[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	want := ledger.QueueEnqueuedPayload{
		Story: first, State: "awaiting_release",
		Branch: gitOut(t, tree, "rev-parse", "--abbrev-ref", "HEAD"), HeadSHA: gitOut(t, tree, "rev-parse", "HEAD"),
	}
	if p != want {
		t.Fatalf("queue entry = %+v, want %+v", p, want)
	}
	if rows[0].BaseID == "" {
		t.Error("the queue entry is an event in the story's chain (it builds on the story's previous event)")
	}

	if err := enterStatus(t, second, "in_progress"); err != nil {
		t.Fatalf("the freed tree admits the second story: %v", err)
	}
	if got := verb.DirtyTreeOwner(ctx, "")(tree); got != second {
		t.Fatalf("only the newly engaged story owns the tree: owner = %q", got)
	}
	if err := enterStatus(t, first, "done"); err != nil {
		t.Fatalf("the wait step moves on to done: %v", err)
	}
}

// AC3, AC4: the queue lists open entries in the order they were recorded; a
// withdrawal needs a reason, records it on the story and drops the entry, and
// moves no status. Under limit = 1 each wait frees the slot for the next.
func TestStoryQueueListsInOrderAndWithdrawsWithAReason(t *testing.T) {
	db, _ := localRepo(t, 1)
	ctx := context.Background()
	var ids []string
	for _, title := range []string{"one", "two", "three"} {
		id := queuedStory(t, title)
		if err := enterStatus(t, id, "in_progress"); err != nil {
			t.Fatalf("engage %s: %v", title, err)
		}
		if err := enterStatus(t, id, "awaiting_release"); err != nil {
			t.Fatalf("queue %s: %v", title, err)
		}
		ids = append(ids, id)
	}
	if got := queueStories(t, ""); strings.Join(got, ",") != strings.Join(ids, ",") {
		t.Fatalf("queue order = %v, want %v", got, ids)
	}
	if got := queueStories(t, "elsewhere"); len(got) != 0 {
		t.Fatalf("--state filters to that wait step: %v", got)
	}

	if _, err := dispatchRaw(t, "story-queue-withdraw", map[string]any{"id": ids[1], "reason": "  "}); err == nil || !strings.Contains(err.Error(), "no reason") {
		t.Fatalf("a withdrawal with no reason is refused: %v", err)
	}
	if _, err := dispatchRaw(t, "story-queue-withdraw", map[string]any{"id": ids[1], "reason": "superseded by a later fix"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if got := queueStories(t, ""); strings.Join(got, ",") != ids[0]+","+ids[2] {
		t.Fatalf("after withdrawing %s the queue = %v", ids[1], got)
	}
	rows, err := db.Ledger.ListByStory(ctx, ids[1], ledger.KindQueueWithdrawn)
	if err != nil || len(rows) != 1 {
		t.Fatalf("want one queue_withdrawn event on the story, got %d (%v)", len(rows), err)
	}
	var w ledger.QueueWithdrawnPayload
	if err := json.Unmarshal(rows[0].Payload, &w); err != nil || w.Reason != "superseded by a later fix" || w.Story != ids[1] {
		t.Fatalf("withdrawal payload = %+v (%v)", w, err)
	}
	it, err := db.Stories.Get(ctx, ids[1])
	if err != nil || it.Status != "awaiting_release" {
		t.Fatalf("a withdrawal moves no status: %q (%v)", it.Status, err)
	}

	if _, err := dispatchRaw(t, "story-queue-withdraw", map[string]any{"id": ids[1], "reason": "again"}); err == nil || !strings.Contains(err.Error(), "no open queue entry") {
		t.Fatalf("withdrawing an entry that is not open is refused: %v", err)
	}
	if _, err := dispatchRaw(t, "story-queue-withdraw", map[string]any{"id": queuedStory(t, "never queued"), "reason": "x"}); err == nil {
		t.Fatal("withdrawing a story that was never queued is refused")
	}
	if _, err := dispatchRaw(t, "ledger-append", map[string]any{"story_id": ids[0], "kind": ledger.KindQueueWithdrawn, "body": "forged"}); err == nil {
		t.Fatal("the generic ledger append must not write a queue event")
	}
}

// A tree with no branch to name refuses the move: the story stays where it was
// and nothing is queued.
func TestWaitStepRefusesATreeWithNoBranch(t *testing.T) {
	db, tree := localRepo(t, 0)
	id := queuedStory(t, "detached")
	if err := enterStatus(t, id, "in_progress"); err != nil {
		t.Fatal(err)
	}
	gitOut(t, tree, "checkout", "--detach")
	err := enterStatus(t, id, "awaiting_release")
	if err == nil || !strings.Contains(err.Error(), "detached HEAD") {
		t.Fatalf("a detached tree is refused: %v", err)
	}
	it, _ := db.Stories.Get(context.Background(), id)
	if it.Status != "in_progress" {
		t.Fatalf("a refused move leaves the story put: %q", it.Status)
	}
	if got := queueStories(t, ""); len(got) != 0 {
		t.Fatalf("a refused move queues nothing: %v", got)
	}
}
