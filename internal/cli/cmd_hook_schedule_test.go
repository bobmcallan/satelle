package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// scheduleFixtureWFs is a route with an epic-parent lane whose waiting step
// declares the child schedule ("" leaves it undeclared).
func scheduleFixtureWFs(schedule string) []docindex.Doc {
	line := ""
	if schedule != "" {
		line = "schedule = \"" + schedule + "\"\n"
	}
	return routeWFs(
		`["*"]
obligations = ["raised", "coded", "closed"]

[epic-parent]
obligations = ["raised", "ready", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[ready]
status = "ready"
waits_on_children = true
`+line+`requires = ["raised"]

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
}

// TestSeatScheduleInject: a scheduled epic container in context puts its
// schedule and the wave command in the session inject; an unscheduled one adds
// nothing, and the worktree clause rides only the parallel schedule (sty_4aefd2a9).
func TestSeatScheduleInject(t *testing.T) {
	epic := workitem.Item{ID: "sty_epic", Category: "epic-parent", Status: "ready"}
	child := workitem.Item{ID: "sty_kid", Category: "fix", Status: "in_progress", ParentID: "sty_epic"}
	other := workitem.Item{ID: "sty_other", Category: "fix", Status: "in_progress"}
	items := []workitem.Item{epic, child, other}

	par := seatScheduleLines([]string{"sty_kid"}, items, scheduleFixtureWFs("parallel"))
	for _, want := range []string{"parallel", "satelle story wave sty_epic", "stop", "worktree per child", "same-tree engagement is refused"} {
		if !strings.Contains(par, want) {
			t.Errorf("parallel inject missing %q: %q", want, par)
		}
	}
	if strings.Contains(par, "sty_kid") {
		t.Errorf("the inject must not name a child: %q", par)
	}
	// The container itself holding the seat is also in context.
	if got := seatScheduleLines([]string{"sty_epic"}, items, scheduleFixtureWFs("parallel")); got != par {
		t.Errorf("container holder: %q want %q", got, par)
	}

	seq := seatScheduleLines([]string{"sty_kid"}, items, scheduleFixtureWFs("sequential"))
	if !strings.Contains(seq, "sequential") || !strings.Contains(seq, "satelle story wave") {
		t.Errorf("sequential inject: %q", seq)
	}
	if strings.Contains(seq, "worktree") {
		t.Errorf("sequential inject must not carry the parallel worktree clause: %q", seq)
	}

	if got := seatScheduleLines([]string{"sty_kid"}, items, scheduleFixtureWFs("")); got != "" {
		t.Errorf("unscheduled container must add no drive-epic text: %q", got)
	}
	if got := seatScheduleLines([]string{"sty_other"}, items, scheduleFixtureWFs("parallel")); got != "" {
		t.Errorf("a holder outside any epic container must add nothing: %q", got)
	}
}

// TestAgentGoalsDriveEpicText: the principle the session receives states both
// drives, the stop rule, the worktree bases, and keeps the one-at-a-time rule
// only for the no-schedule case (sty_4aefd2a9 AC1, AC3, AC5).
func TestAgentGoalsDriveEpicText(t *testing.T) {
	body, ok := embeddedDefault("principles", "satelle-agent-goals")
	if !ok {
		t.Fatal("satelle-agent-goals embedded default not present")
	}
	flat := strings.Join(strings.Fields(body), " ")
	for _, want := range []string{
		"Drive epic `<id>` to ready** walks only the container to ready. It does not engage children.",
		"Drive epic `<id>` to done** requires the container at its waiting step",
		"`satelle story wave <id>` returns the set that may be engaged",
		"only after the wave is empty because every child is terminal",
		"When `satelle story wave` exits non-zero, that is a **stop**",
		"Do not pick a child by `order:`, by the sprint, or by title",
		"Cut an independent child from the epic base",
		"from that target's branch, not from main",
		"A cancelled dependency is a stop",
		"One story at a time — when the container declares no schedule.",
		"This is not the rule once a container declares a schedule",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("principle missing %q", want)
		}
	}
	// Every one-at-a-time statement is qualified by the no-schedule case.
	lower := strings.ToLower(flat)
	for from := 0; ; {
		i := strings.Index(lower[from:], "one at a time")
		if i < 0 {
			break
		}
		i += from
		lo, hi := max(0, i-120), min(len(lower), i+160)
		if !strings.Contains(lower[lo:hi], "schedule") {
			t.Errorf("unqualified one-at-a-time: %q", flat[lo:hi])
		}
		from = i + 1
	}
}
