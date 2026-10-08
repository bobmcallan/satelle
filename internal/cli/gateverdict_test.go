package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/gatehandle"
	"github.com/bobmcallan/satelle/internal/verb"
)

// sty_c4b92c9e: what a finished gate hands the driving session is the verdict —
// the handle, the command, each gate's decision — and nothing the command
// printed besides. Every byte of the rest is tokens the session pays to read and
// cannot act on.

func TestVerdictBlock_IsTheVerdictAndNothingElse(t *testing.T) {
	v := gatehandle.Verdict{
		Meta:   gatehandle.Meta{ID: "gw_1", Verb: "story-set", Story: "sty_1", Argv: []string{"story", "set", "sty_1", "--status", "in_progress"}},
		Lines:  "accepted backlog→in_progress by slow-check: decision=accept notes=fine\n",
		Stdout: `{"id":"sty_1","title":"the whole story record","acceptance_criteria":"1. …"}`,
		Stderr: "note: log a step-self-report for the step just left\n",
	}
	got := renderGateVerdict(v)
	for _, want := range []string{"gw_1", "`satelle story set sty_1 --status in_progress`", "sty_1 completed", "accepted backlog→in_progress by slow-check"} {
		if !strings.Contains(got, want) {
			t.Errorf("delivery lacks %q:\n%s", want, got)
		}
	}
	for _, leaked := range []string{"acceptance_criteria", "the whole story record", "step-self-report"} {
		if strings.Contains(got, leaked) {
			t.Errorf("delivery carries %q — not part of the verdict:\n%s", leaked, got)
		}
	}
}

// A create's product is the one thing the driver cannot fetch by name: its id
// rides in the verdict block, without the record around it.
func TestVerdictBlock_ACreateReturnsItsIDNotItsRecord(t *testing.T) {
	v := gatehandle.Verdict{
		Meta:   gatehandle.Meta{ID: "gw_2", Verb: "story-create", Argv: []string{"story", "create"}},
		Lines:  "accepted story create by satelle-story-review: decision=accept notes=ok\n",
		Stdout: `{"id":"sty_new","title":"Add a widget","body":"long body"}`,
	}
	got := verdictBlock(v)
	if !strings.Contains(got, "created: sty_new") || !strings.Contains(got, "accepted story create") {
		t.Fatalf("a create's block = %q, want the verdict line and its id", got)
	}
	if strings.Contains(got, "Add a widget") || strings.Contains(got, "long body") {
		t.Fatalf("a create's block carries the story record:\n%s", got)
	}
	// A failed create made nothing: no id, whatever it printed.
	v.Result = gatehandle.Result{ExitCode: 1, Error: "story rejected by satelle-story-review: no acceptance criteria"}
	if got := verdictBlock(v); strings.Contains(got, "created:") || !strings.Contains(got, "no acceptance criteria") {
		t.Fatalf("a failed create's block = %q, want the rejection and no id", got)
	}
}

// A reject arrives as the run's error and carries the reviewer's notes.
func TestVerdictBlock_ARejectCarriesTheReviewersNotes(t *testing.T) {
	v := gatehandle.Verdict{
		Meta:   gatehandle.Meta{ID: "gw_3", Argv: []string{"story", "set", "sty_3"}, Story: "sty_3"},
		Result: gatehandle.Result{ExitCode: 1, Error: "rejected plan→in_progress by satelle-story-plan-review: the plan names no tests"},
		Stdout: `{"id":"sty_3"}`,
	}
	got := renderGateVerdict(v)
	if !strings.Contains(got, "FAILED") || !strings.Contains(got, "the plan names no tests") || strings.Contains(got, `"id":"sty_3"`) {
		t.Fatalf("reject block:\n%s", got)
	}
}

// The verdict recorder is what a detached run writes: EmitVerdict shows AND
// records; RecordVerdict (an accepted create or amend) records without showing.
func TestVerdictRecorder_ShowsOnlyWhatItAlwaysShowed(t *testing.T) {
	withVerbWiring(t)
	path := t.TempDir() + "/verdict.log"
	verb.SetVerdictRecorder(appendLine(path))

	shown := captureStderr(t, func() {
		verb.EmitVerdict("accepted a→b by g: decision=accept notes=n")
		verb.RecordVerdict("accepted story create by g: decision=accept notes=n")
	})
	if !strings.Contains(shown, "accepted a→b") || strings.Contains(shown, "story create") {
		t.Fatalf("stderr = %q: a transition verdict is shown as before, a silent create accept stays silent", shown)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "accepted a→b") || !strings.Contains(string(b), "accepted story create") {
		t.Fatalf("recorded = %q, want both verdict lines", b)
	}
}

// The hook records each delivery as a driver-usage row through the seam, once per
// delivered run, and never for a run already delivered.
func TestGateDelivery_RecordsEachDeliveryOnce(t *testing.T) {
	_ = tempRepo(t)
	clearHarnessEnv(t)
	var got []gatehandle.Meta
	old := recordGateDelivered
	recordGateDelivered = func(delivered []gatehandle.Meta) { got = append(got, delivered...) }
	t.Cleanup(func() { recordGateDelivered = old })

	store := gateStoreForTest(t)
	m, _ := store.Create(gatehandle.Meta{Verb: "story-set", Story: "sty_rec", Argv: []string{"story", "set", "sty_rec"}})
	_ = store.Finish(m.ID, gatehandle.Result{})

	if text := gateDeliveryFor(0); !strings.Contains(text, m.ID) {
		t.Fatalf("nothing delivered: %q", text)
	}
	if len(got) != 1 || got[0].ID != m.ID || got[0].Story != "sty_rec" {
		t.Fatalf("delivery rows recorded for %+v, want exactly %s", got, m.ID)
	}
	if text := gateDeliveryFor(0); text != "" || len(got) != 1 {
		t.Fatalf("a delivered run was recorded again: text=%q recorded=%d", text, len(got))
	}
}

// restoringRecorder wraps the real delivery recorder so a test that reaches it
// (gateDeliveryFor, stopGateDeliveryFor, runGateResume with no stub) does not
// leave behind the ledger it wired: the recorder opens a store, points verb at
// it and closes it, and nothing else puts the prior wiring back. TestMain
// installs it; production keeps the unwrapped recorder — a hook is its own
// process.
func restoringRecorder(real func([]gatehandle.Meta)) func([]gatehandle.Meta) {
	return func(delivered []gatehandle.Meta) {
		restore, _ := verb.SnapshotWiring()
		defer restore()
		real(delivered)
	}
}
