package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/costview"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// gateValueRepo seeds a repo with an epic (parent → child) and an unrelated
// story, each carrying one gate invocation ($1 / $2 / $8) and a reject, all
// stamped at `at`. It returns the parent and child ids.
func gateValueRepo(t *testing.T, at time.Time) (parentID, childID string) {
	t.Helper()
	_ = tempRepo(t)
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	ctx := context.Background()
	mk := func(title, parent string) string {
		it, err := a.Store.Stories.Create(ctx, workitem.CreateInput{
			Kind: workitem.KindStory, Title: title, Status: "backlog", ParentID: parent,
		}, at)
		if err != nil {
			t.Fatal(err)
		}
		return it.ID
	}
	parentID = mk("epic", "")
	childID = mk("child", parentID)
	otherID := mk("unrelated", "")

	for id, cost := range map[string]float64{parentID: 1, childID: 2, otherID: 8} {
		inv, _ := json.Marshal(map[string]any{
			"from": "a", "to": "b", "agent": "reviewer", "model": "opus", "skill": "gate-a",
			"usage_available": true, "tokens_in_fresh": 10, "tokens_out": 5, "cost_usd": cost,
		})
		verdict, _ := json.Marshal(map[string]any{"skill": "gate-a", "agent": "reviewer", "model": "opus", "accept": false})
		for _, in := range []ledger.AppendInput{
			{StoryID: id, Kind: ledger.KindAgentInvocation, Actor: "reviewer", Payload: inv},
			{StoryID: id, Kind: ledger.KindReviewReject, Actor: "reviewer", Payload: verdict},
		} {
			if _, err := a.Store.Ledger.Append(ctx, in, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	return parentID, childID
}

func runGateValueJSON(t *testing.T, args ...string) costview.GateValueReport {
	t.Helper()
	out, err := runRoot(t, append([]string{"story", "cost", "--gate-value", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("story cost --gate-value %v: %v\n%s", args, err, out)
	}
	var rep costview.GateValueReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("parse gate-value json: %v\n%s", err, out)
	}
	return rep
}

func gateValueSum(rep costview.GateValueReport) (invocations, rejects int, usd float64) {
	for _, r := range rep.Rows {
		if r.Skill == "gate-a" {
			invocations += r.Invocations
			rejects += r.Rejects
			usd += r.CostUSD
		}
	}
	return invocations, rejects, usd
}

// TestStoryCostGateValueScopesAndDates pins the `story cost --gate-value`
// wiring end to end (sty_b8542a3a AC6): --epic scopes to the epic family (root
// and descendants, not an unrelated story), --story to one story, no scope to
// the whole repo; --since/--until bound the date range with --until covering
// the WHOLE named day; and --json round-trips the same report the table renders.
func TestStoryCostGateValueScopesAndDates(t *testing.T) {
	at := time.Now().UTC()
	parentID, childID := gateValueRepo(t, at)
	today := at.Format("2006-01-02")
	yesterday := at.Add(-24 * time.Hour).Format("2006-01-02")
	tomorrow := at.Add(24 * time.Hour).Format("2006-01-02")

	if inv, rej, usd := gateValueSum(runGateValueJSON(t)); inv != 3 || rej != 3 || usd != 11 {
		t.Errorf("repo-wide = %d inv / %d rej / $%v, want 3 / 3 / $11", inv, rej, usd)
	}
	if inv, rej, usd := gateValueSum(runGateValueJSON(t, "--epic", parentID)); inv != 2 || rej != 2 || usd != 3 {
		t.Errorf("--epic = %d inv / %d rej / $%v, want the parent+child only: 2 / 2 / $3", inv, rej, usd)
	}
	if inv, _, usd := gateValueSum(runGateValueJSON(t, "--story", childID)); inv != 1 || usd != 2 {
		t.Errorf("--story = %d inv / $%v, want the child only: 1 / $2", inv, usd)
	}

	// --until <today> includes entries stamped any time today (the whole day).
	if inv, _, _ := gateValueSum(runGateValueJSON(t, "--epic", parentID, "--since", today, "--until", today)); inv != 2 {
		t.Errorf("--since/--until today = %d invocations, want 2 (--until covers the whole day)", inv)
	}
	// A window ending yesterday, or starting tomorrow, excludes them.
	if inv, rej, _ := gateValueSum(runGateValueJSON(t, "--epic", parentID, "--until", yesterday)); inv != 0 || rej != 0 {
		t.Errorf("--until yesterday = %d inv / %d rej, want none", inv, rej)
	}
	if inv, _, _ := gateValueSum(runGateValueJSON(t, "--since", tomorrow)); inv != 0 {
		t.Errorf("--since tomorrow = %d invocations, want none", inv)
	}

	// Table form: same data, dollars-per-reject from the known-dollar subtotal.
	out, err := runRoot(t, "story", "cost", "--gate-value", "--epic", parentID)
	if err != nil {
		t.Fatalf("table: %v\n%s", err, out)
	}
	for _, want := range []string{"SKILL", "$/REJECT", "gate-a", "reviewer@opus", "$3.00", "$1.50"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}

	// A bad date is a usage error, not an empty report.
	if out, err := runRoot(t, "story", "cost", "--gate-value", "--since", "not-a-date"); err == nil {
		t.Errorf("--since not-a-date should fail, got:\n%s", out)
	}
}
