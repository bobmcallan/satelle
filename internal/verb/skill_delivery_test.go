package verb_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_23ceeca2: the verb records each skill delivered to a performer on its
// invocation row, shows an in-loop session what it was handed, and applies the
// skill-evidence check's answer.

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	_ = w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

type deliveryRow struct {
	Agent         string               `json:"agent"`
	Kind          string               `json:"kind"`
	Skills        []verb.SkillDelivery `json:"skills"`
	SkillEvidence *verb.EvidenceReport `json:"skill_evidence"`
	Actor         string               `json:"-"`
}

// deliveryRows reads the rows that record a skill delivery: a dispatched
// performer's agent_invocation row and an in-loop session's skill_delivery row.
func deliveryRows(t *testing.T, ls *ledger.Store, id string) []deliveryRow {
	t.Helper()
	var entries []ledger.Entry
	for _, kind := range []string{ledger.KindAgentInvocation, ledger.KindSkillDelivery} {
		es, err := ls.ListByStory(context.Background(), id, kind)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, es...)
	}
	var out []deliveryRow
	for _, e := range entries {
		var r deliveryRow
		if json.Unmarshal(e.Payload, &r) == nil {
			r.Actor = e.Actor
			out = append(out, r)
		}
	}
	return out
}

func newBacklogStory(t *testing.T) workitem.Item {
	t.Helper()
	var it workitem.Item
	json.Unmarshal(call(t, "story-create", map[string]any{"title": "x", "status": "backlog"}), &it)
	return it
}

var deliveredA = verb.SkillDelivery{Name: "skill-a", Hash: "hash-of-a", Delivered: true}

// AC1: a dispatched performer's invocation row lists each delivered skill.
func TestDispatchedInvocationRowRecordsSkills(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetExecutorDispatcher(&dispatcherStub{res: verb.DispatchResult{
		Dispatched: true, Agent: "coder", Skill: "skill-a", Skills: []verb.SkillDelivery{deliveredA},
	}})
	it := newBacklogStory(t)
	if _, err := dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"}); err != nil {
		t.Fatal(err)
	}
	rows := deliveryRows(t, db.Ledger, it.ID)
	if len(rows) != 1 || rows[0].Agent != "coder" || len(rows[0].Skills) != 1 || rows[0].Skills[0] != deliveredA {
		t.Fatalf("rows = %+v, want one coder row carrying skill-a and its hash", rows)
	}
}

// AC1: an in-loop step's engagement writes an in-loop row with the same skills
// field and shows the session the rubric, once.
func TestInLoopEntryRecordsSkillsAndShowsTheRubric(t *testing.T) {
	withWiring(t)
	db := wire(t)
	verb.SetExecutorDispatcher(&dispatcherStub{res: verb.DispatchResult{
		Kind: verb.PerformerExecutor, Skills: []verb.SkillDelivery{deliveredA}, InLoopRubric: "RUBRIC-TEXT-A",
		TraceCommands: true,
	}})
	it := newBacklogStory(t)
	var err error
	shown := captureStderr(t, func() {
		_, err = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := deliveryRows(t, db.Ledger, it.ID)
	if len(rows) != 1 || rows[0].Actor != "in-loop" || rows[0].Kind != string(verb.PerformerExecutor) ||
		len(rows[0].Skills) != 1 || rows[0].Skills[0] != deliveredA {
		t.Fatalf("rows = %+v, want one in-loop row carrying skill-a and its hash", rows)
	}
	if strings.Count(shown, "RUBRIC-TEXT-A") != 1 {
		t.Errorf("stderr = %q, want the rubric shown exactly once", shown)
	}
	// The entry row tells the gate hooks what to record: this step declares commands.
	entries, err := db.Ledger.ListByStory(context.Background(), it.ID, ledger.KindSkillDelivery)
	if err != nil || len(entries) != 1 {
		t.Fatalf("skill_delivery rows = %d, err %v", len(entries), err)
	}
	var flags struct {
		TraceCommands bool `json:"trace_commands"`
		TraceTools    bool `json:"trace_tools"`
	}
	if json.Unmarshal(entries[0].Payload, &flags) != nil || !flags.TraceCommands || flags.TraceTools {
		t.Errorf("entry row flags = %+v, want trace_commands only", flags)
	}
}

// AC2: an in-loop skill that did not resolve is recorded undelivered and flagged;
// under strict it also refuses the entry, leaving the status unchanged.
func TestInLoopUndeliveredSkill(t *testing.T) {
	missing := verb.SkillDelivery{Name: "skill-a", Reason: "unresolved"}
	for _, strict := range []bool{false, true} {
		withWiring(t)
		db := wire(t)
		verb.SetExecutorDispatcher(&dispatcherStub{res: verb.DispatchResult{
			Kind: verb.PerformerInLoop, Skills: []verb.SkillDelivery{missing}, StrictSkills: strict,
		}})
		it := newBacklogStory(t)
		var err error
		shown := captureStderr(t, func() {
			_, err = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
		})
		rows := deliveryRows(t, db.Ledger, it.ID)
		if len(rows) == 0 || len(rows[0].Skills) != 1 || rows[0].Skills[0].Delivered {
			t.Fatalf("strict=%v: rows = %+v, want skill-a recorded undelivered", strict, rows)
		}
		if !strings.Contains(shown, "skill-a") || !strings.Contains(shown, "was not delivered") {
			t.Errorf("strict=%v: stderr = %q, want the gap shown", strict, shown)
		}
		var after workitem.Item
		json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &after)
		if strict && (err == nil || after.Status != "backlog") {
			t.Errorf("strict: err=%v status=%q, want a refusal that leaves backlog", err, after.Status)
		}
		if !strict && (err != nil || after.Status != "in_progress") {
			t.Errorf("record: err=%v status=%q, want the entry to proceed", err, after.Status)
		}
	}
}

// AC5: a dispatched performer's evidence gap is recorded on its row and shown by
// default, and refuses the transition when the workflow says reject.
func TestDispatchedEvidenceGapRecordedOrRefused(t *testing.T) {
	gap := verb.EvidenceItem{Skill: "coder", Kind: verb.EvidenceCommand, Name: "go test", Status: verb.EvidenceMissing}
	for _, strict := range []bool{false, true} {
		withWiring(t)
		db := wire(t)
		verb.SetExecutorDispatcher(&dispatcherStub{res: verb.DispatchResult{
			Dispatched: true, Agent: "coder", Skills: []verb.SkillDelivery{deliveredA},
			Evidence: &verb.EvidenceReport{Items: []verb.EvidenceItem{gap}, Strict: strict},
		}})
		it := newBacklogStory(t)
		var err error
		shown := captureStderr(t, func() {
			_, err = dispatchRaw(t, "story-set", map[string]any{"id": it.ID, "status": "in_progress"})
		})
		rows := deliveryRows(t, db.Ledger, it.ID)
		if len(rows) == 0 || rows[0].SkillEvidence == nil || len(rows[0].SkillEvidence.Items) != 1 {
			t.Fatalf("strict=%v: rows = %+v, want the gap on the invocation row", strict, rows)
		}
		if !strings.Contains(shown, `missing command "go test"`) {
			t.Errorf("strict=%v: stderr = %q, want the gap named", strict, shown)
		}
		var after workitem.Item
		json.Unmarshal(call(t, "story-get", map[string]any{"id": it.ID}), &after)
		if strict && (err == nil || !strings.Contains(err.Error(), "skill-evidence") || after.Status != "backlog") {
			t.Errorf("reject: err=%v status=%q, want a skill-evidence refusal that leaves backlog", err, after.Status)
		}
		if !strict && (err != nil || after.Status != "in_progress") {
			t.Errorf("record: err=%v status=%q, want the transition to proceed", err, after.Status)
		}
	}
}

func TestEvidenceReportRendering(t *testing.T) {
	r := verb.EvidenceReport{
		Undelivered: []string{"gone"},
		Items: []verb.EvidenceItem{
			{Skill: "s", Kind: verb.EvidenceSection, Name: "A", Status: verb.EvidenceMet},
			{Skill: "s", Kind: verb.EvidenceCommand, Name: "go test", Status: verb.EvidenceMissing},
			{Skill: "s", Kind: verb.EvidenceTool, Name: "Read", Status: verb.EvidenceUnverifiable, Detail: "adapter pi: no trace channel"},
		},
	}
	gaps := strings.Join(r.Gaps(), "|")
	if !strings.Contains(gaps, `skill "gone" was not delivered`) || !strings.Contains(gaps, `s: missing command "go test"`) || strings.Contains(gaps, "Read") {
		t.Errorf("Gaps = %q", gaps)
	}
	if un := r.Unverifiable(); len(un) != 1 || !strings.Contains(un[0], "adapter pi") {
		t.Errorf("Unverifiable = %v", un)
	}
	if r.Blocks() {
		t.Error("a non-strict report must not block")
	}
	r.Strict = true
	if !r.Blocks() || r.Verdict(2).Accept || r.Verdict(2).Order != 2 || !r.Verdict(2).System {
		t.Errorf("strict verdict = %+v, want a rejecting system verdict at order 2", r.Verdict(2))
	}
	clean := verb.EvidenceReport{Strict: true, Items: []verb.EvidenceItem{{Skill: "s", Kind: verb.EvidenceKey, Name: "k", Status: verb.EvidenceMet}}}
	if clean.Blocks() || !clean.Verdict(0).Accept {
		t.Error("a met report must accept even when strict")
	}
}
