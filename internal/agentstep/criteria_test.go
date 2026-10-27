package agentstep

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentartifact"
	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_99d3162b: a seat's verdict carries one entry per acceptance criterion, a
// settled criterion keeps its accept while its text is unchanged, and each panel
// seat is shown the other seats' open findings.

// ---- AC1: parsing -----------------------------------------------------------

func TestParseDecisionCriteria(t *testing.T) {
	over := strings.Repeat("x", reviewedCeiling+1)
	out := fmt.Sprintf(`{"decision":"accept","notes":"n","criteria":[
		{"criterion":3,"status":"ACCEPT","finding":"f3","reviewed":"Q3","text":"model-sent text"},
		{"criterion":"AC2","status":"reject","finding":"f2"},
		{"criterion":"1","status":"bogus"},
		{"criterion":-4,"status":"accept"},
		{"criterion":"nonsense","status":"accept"},
		{"criterion":0,"status":"reject","finding":"general"},
		{"criterion":5,"status":"accept","reviewed":%q},
		{"criterion":3,"status":"reject","finding":"later entry wins"}
	]}`, over)
	dec, err := parseDecision([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []verb.CriterionVerdict{
		{Criterion: 0, Status: verb.CriterionRejected, Finding: "general"},
		{Criterion: 1, Status: verb.CriterionUnchecked},
		{Criterion: 2, Status: verb.CriterionRejected, Finding: "f2"},
		{Criterion: 3, Status: verb.CriterionRejected, Finding: "later entry wins"},
		{Criterion: 5, Status: verb.CriterionAccepted, ReviewedTruncated: true},
	}
	if len(dec.Criteria) != len(want) {
		t.Fatalf("criteria = %+v, want %+v", dec.Criteria, want)
	}
	for i := range want {
		if dec.Criteria[i] != want[i] {
			t.Errorf("criteria[%d] = %+v, want %+v", i, dec.Criteria[i], want[i])
		}
	}
	if !dec.Accept {
		t.Error("parse records the top-level decision; the criteria decide later")
	}
}

func TestParseDecisionCapsTheCriteria(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"decision":"accept","criteria":[`)
	for i := 1; i <= maxCriteria+30; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"criterion":%d,"status":"accept"}`, i)
	}
	b.WriteString(`]}`)
	dec, err := parseDecision([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Criteria) != maxCriteria || dec.Criteria[len(dec.Criteria)-1].Criterion != maxCriteria {
		t.Errorf("kept %d entries, last %+v, want the lowest %d", len(dec.Criteria), dec.Criteria[len(dec.Criteria)-1], maxCriteria)
	}
}

func TestParseDecisionWithoutCriteriaHasNone(t *testing.T) {
	dec, err := parseDecision([]byte(`{"decision":"reject","notes":"n"}`))
	if err != nil || dec.Criteria != nil || dec.Accept {
		t.Errorf("dec = %+v, err = %v", dec, err)
	}
}

func TestParseBundleDecisionsCriteriaPerVerdict(t *testing.T) {
	out := `{"verdicts":[
		{"skill":"rev-a","decision":"accept","criteria":[{"criterion":1,"status":"accept"}]},
		{"skill":"rev-b","decision":"reject","criteria":[{"criterion":1,"status":"reject","finding":"b1"}]}]}`
	decs, err := parseBundleDecisions([]byte(out), []string{"rev-a", "rev-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(decs[0].Criteria) != 1 || decs[0].Criteria[0].Status != verb.CriterionAccepted ||
		len(decs[1].Criteria) != 1 || decs[1].Criteria[0].Finding != "b1" {
		t.Errorf("each bundled verdict keeps its own criteria: %+v", decs)
	}
}

// ---- AC2: the criteria judged ----------------------------------------------

func TestJudgedCriteria(t *testing.T) {
	perf := verb.PerformerResolution{OutputName: "plan", CriteriaSection: "Acceptance criteria"}
	plan := DocState{Name: "plan", Type: "plan", Body: "## Design\nd\n\n## Acceptance criteria\n1. plan one\n2. plan two\n3. plan three\n\n## Risks\nr\n"}
	story := workitem.Item{AcceptanceCriteria: "1. story one\n2. story two"}

	t.Run("a plan section with more criteria than the story", func(t *testing.T) {
		got, src := judgedCriteria(story, []DocState{plan}, perf)
		if len(got) != 3 || got[2].Text != "plan three" || src != "doc:plan#Acceptance criteria" {
			t.Errorf("got %+v from %q", got, src)
		}
	})
	t.Run("the story carries none", func(t *testing.T) {
		got, src := judgedCriteria(workitem.Item{}, []DocState{plan}, perf)
		if len(got) != 3 || src != "doc:plan#Acceptance criteria" {
			t.Errorf("got %+v from %q", got, src)
		}
	})
	t.Run("no section is declared", func(t *testing.T) {
		got, src := judgedCriteria(story, []DocState{plan}, verb.PerformerResolution{OutputName: "plan"})
		if len(got) != 2 || got[0].Text != "story one" || src != criteriaSourceStory {
			t.Errorf("got %+v from %q", got, src)
		}
	})
	t.Run("the section is declared but empty", func(t *testing.T) {
		empty := DocState{Name: "plan", Body: "## Acceptance criteria\n\n## Risks\nr\n"}
		got, src := judgedCriteria(story, []DocState{empty}, perf)
		if len(got) != 2 || src != criteriaSourceStory {
			t.Errorf("got %+v from %q", got, src)
		}
	})
	t.Run("the document is not attached", func(t *testing.T) {
		got, src := judgedCriteria(story, nil, perf)
		if len(got) != 2 || src != criteriaSourceStory {
			t.Errorf("got %+v from %q", got, src)
		}
	})
	t.Run("nothing numbered anywhere", func(t *testing.T) {
		if got, src := judgedCriteria(workitem.Item{AcceptanceCriteria: "prose only"}, nil, perf); got != nil || src != "" {
			t.Errorf("got %+v from %q", got, src)
		}
	})
}

func TestStepPerformerReportsTheCriteriaSection(t *testing.T) {
	wf := spineWF("", "", "", "named|architect|plan-skill", "done")
	g, _ := newEngine(t, "ok", fakeDocs{workflow: wf, extraSkills: []docindex.Doc{{Kind: "skills", Name: "plan-skill", Body: critPlanSkill}}})
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		return config.AgentBinding{Command: "fake -p {system}", Tools: "Read"}, name == "architect"
	})
	got, err := g.StepPerformer(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "named")
	if err != nil {
		t.Fatal(err)
	}
	if got.OutputName != "plan" || got.CriteriaSection != "Acceptance criteria" {
		t.Errorf("StepPerformer = %+v, want the skill's output name and criteria section", got)
	}
}

// ---- fixtures ---------------------------------------------------------------

const critPlanSkill = "---\nname: plan-skill\ntype: skill\ndescription: planner\noutput_name: plan\noutput_type: plan\noutput_criteria_section: Acceptance criteria\n---\n# plan\nPlan.\n"
const critPlanSkillNoSection = "---\nname: plan-skill\ntype: skill\ndescription: planner\noutput_name: plan\noutput_type: plan\n---\n# plan\nPlan.\n"

// critPlan is a plan document whose premise quotations are PREMISE-1..3 and whose
// criteria are the given lines.
func critPlan(criteria ...string) string {
	return "# Plan\n\n## Premise check\nPREMISE-1 holds. PREMISE-2 holds. PREMISE-3 holds.\n\n## Acceptance criteria\n" +
		strings.Join(criteria, "\n") + "\n\n## Risks\nnone\n"
}

var critLines = []string{"1. first criterion", "2. second criterion", "3. third criterion"}

// scriptRunner answers every call with out and keeps the work item it opened.
type scriptRunner struct {
	mu      sync.Mutex
	command string
	out     string
	shots   []boundShot
}

func (r *scriptRunner) Name() string    { return "script-fake" }
func (r *scriptRunner) Command() string { return r.command }
func (r *scriptRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	r.mu.Lock()
	r.shots = append(r.shots, boundShot{payload: req.Payload, opened: openPayloadFiles(req.Payload)})
	r.mu.Unlock()
	return []byte(r.out), nil
}

// payload is the inline work item the n-th call opened.
func (r *scriptRunner) payload(t *testing.T, n int) (state []criterionState, peers []peerFinding, source string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if n >= len(r.shots) {
		t.Fatalf("%s opened %d work items, want at least %d", r.command, len(r.shots), n+1)
	}
	var p struct {
		CriteriaSource string           `json:"criteria_source"`
		CriteriaState  []criterionState `json:"criteria_state"`
		PeerFindings   []peerFinding    `json:"peer_findings"`
	}
	if err := json.Unmarshal([]byte(r.shots[n].payload), &p); err != nil {
		t.Fatal(err)
	}
	return p.CriteriaState, p.PeerFindings, p.CriteriaSource
}

func (r *scriptRunner) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.shots)
}

// foldRaw records the combine check's stdin and accepts only when every seat did.
type foldRaw struct {
	mu       sync.Mutex
	payloads []string
}

func (f *foldRaw) check(_ context.Context, _, _, payload string) (string, error) {
	f.mu.Lock()
	f.payloads = append(f.payloads, payload)
	f.mu.Unlock()
	var p struct {
		Seats []struct {
			Accept      bool `json:"accept"`
			Unavailable bool `json:"unavailable"`
		} `json:"seats"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return "", err
	}
	for _, s := range p.Seats {
		if !s.Accept || s.Unavailable {
			return "a seat did not accept\n", errFakeExit
		}
	}
	return "ok\n", nil
}

// critFixture is a backlog → plan edge performed by a planner, whose plan document
// holds the criteria under judgment, gated by rev-a — one reviewer, or a panel of
// s1 and s2.
type critFixture struct {
	g     *Engine
	solo  *scriptRunner
	s1    *scriptRunner
	s2    *scriptRunner
	fold  *foldRaw
	plan  string
	item  workitem.Item
	prior []PriorVerdict
}

type critOpts struct {
	panel, carry bool
	planSkill    string
	plan         string
	storyAC      string
	prior        []PriorVerdict
	solo, s1, s2 string // the JSON each reviewer answers with
	// requireCriteria gives the reviewer rubric `verdict_criteria: required`.
	requireCriteria bool
}

func newCritFixture(t *testing.T, o critOpts) *critFixture {
	t.Helper()
	if o.planSkill == "" {
		o.planSkill = critPlanSkill
	}
	extra, ragent := carryKeyIf(o.carry), "reviewer"
	if o.panel {
		extra = "panel = [\"s1\", \"s2\"]\ncombine = \"fold\"" + carryKeyIf(o.carry)
		ragent = ""
	}
	wf := spineWF("", "", "", "plan|planner|plan-skill|rev-a|"+ragent+"||"+extra, "done")
	rubrics := bundleSkillDocs("rev-a")
	if o.requireCriteria {
		rubrics[0].Body = strings.Replace(rubrics[0].Body, "description: parallel test gate\n", "description: parallel test gate\nverdict_criteria: required\n", 1)
	}
	skills := append(rubrics,
		docindex.Doc{Kind: "skills", Name: "fold", Body: panelFoldSkill},
		docindex.Doc{Kind: "skills", Name: "plan-skill", Body: o.planSkill})
	f := &critFixture{
		solo: &scriptRunner{command: "solo-fake -p {system}", out: o.solo},
		s1:   &scriptRunner{command: "seat-s1 -p {system}", out: o.s1},
		s2:   &scriptRunner{command: "seat-s2 -p {system}", out: o.s2},
		fold: &foldRaw{}, plan: o.plan, prior: o.prior,
		item: workitem.Item{ID: "sty_c", Status: "backlog", Category: "feature", Title: "T", Body: "B", AcceptanceCriteria: o.storyAC},
	}
	f.g = New(f.solo, fakeDocs{workflow: wf, extraSkills: skills}, "/repo", "")
	f.g.backoff = func(int) time.Duration { return 0 }
	f.g.check = f.fold.check
	f.g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		switch name {
		case "planner":
			return config.AgentBinding{Command: "planner-fake -p {system}", Tools: "Read"}, true
		case "s1":
			return config.AgentBinding{Command: f.s1.command, Role: config.RoleReviewer, Tools: "Read"}, true
		case "s2":
			return config.AgentBinding{Command: f.s2.command, Role: config.RoleReviewer, Tools: "Read"}, true
		}
		return config.AgentBinding{}, false
	})
	f.g.newRunner = func(_, command string) (agentcli.Runner, error) {
		if strings.HasPrefix(command, "seat-s1") {
			return f.s1, nil
		}
		return f.s2, nil
	}
	f.g.SetDocsResolver(func(context.Context, string) []DocState {
		return []DocState{{Name: "plan", Type: "plan", Body: f.plan}}
	})
	f.g.SetPriorVerdictsResolver(func(context.Context, string, string, string, verb.StaleExemption) []PriorVerdict {
		return append([]PriorVerdict(nil), f.prior...)
	})
	return f
}

func carryKeyIf(on bool) string {
	if on {
		return "\n" + carryKey
	}
	return ""
}

func (f *critFixture) gate(t *testing.T) (verb.GateDecision, verb.ReviewerVerdict) {
	t.Helper()
	dec, err := f.g.Gate(context.Background(), f.item, "plan")
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	return dec, reviewerOf(t, dec, "rev-a")
}

func critEntry(n int, status, finding string) map[string]any {
	return map[string]any{"criterion": n, "status": status, "finding": finding}
}

func verdictJSON(decision string, entries ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"decision": decision, "notes": "NOTES", "criteria": entries})
	return string(b)
}

func entryOf(t *testing.T, entries []verb.CriterionVerdict, n int) verb.CriterionVerdict {
	t.Helper()
	e, ok := entryFor(entries, n)
	if !ok {
		t.Fatalf("no entry for criterion %d in %+v", n, entries)
	}
	return e
}

// ---- AC1 + AC2 + AC3: the judged criteria and the decision they derive -------

func TestGateDecidesFromTheCriteria(t *testing.T) {
	acc := func(n int) map[string]any { return critEntry(n, "accept", "ok") }
	cases := []struct {
		name   string
		out    string
		accept bool
		notes  string
	}{
		{"a top-level accept with every criterion accepted", verdictJSON("accept", acc(1), acc(2), acc(3)), true, ""},
		{"a top-level accept with a rejected criterion", verdictJSON("accept", acc(1), critEntry(2, "reject", "bad"), acc(3)), false, "AC2"},
		{"a top-level accept with an unchecked criterion", verdictJSON("accept", acc(1), acc(2), critEntry(3, "unchecked", "")), false, "AC3"},
		{"a top-level accept with a criterion omitted", verdictJSON("accept", acc(1), acc(3)), false, "AC2"},
		{"a top-level accept with a general reject", verdictJSON("accept", acc(0), critEntry(0, "reject", "outside"), acc(1), acc(2), acc(3)), false, "general objection"},
		{"a top-level reject with every criterion accepted", verdictJSON("reject", acc(1), acc(2), acc(3)), true, "top-level reject"},
		{"a verdict with no criteria is decided as before", `{"decision":"accept","notes":"NOTES"}`, true, ""},
		{"a verdict with no criteria rejects as before", `{"decision":"reject","notes":"NOTES"}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: tc.out})
			dec, rv := f.gate(t)
			if dec.Accept != tc.accept || rv.Accept != tc.accept {
				t.Fatalf("accept = %v / %v, want %v: %+v", dec.Accept, rv.Accept, tc.accept, rv)
			}
			if tc.notes != "" && !strings.Contains(rv.Notes, tc.notes) {
				t.Errorf("notes %q should name %q", rv.Notes, tc.notes)
			}
			if tc.notes == "" && !strings.Contains(rv.Notes, "NOTES") {
				t.Errorf("notes %q lost the reviewer's own", rv.Notes)
			}
		})
	}
}

// A verdict that sent criteria[] decides from it even when no entry survives
// normalising: every number unparseable ("AC-2", "AC2."), or none of them judged.
// The top-level accept must never stand in for criteria that were not recorded.
var unusableCriteria = []struct{ name, entries string }{
	{"unparseable numbers", `[{"criterion":"AC-1","status":"accept"},{"criterion":"AC-2","status":"reject","finding":"x"},{"criterion":"AC3.","status":"accept"}]`},
	{"out-of-range numbers", `[{"criterion":9,"status":"accept"}]`},
	{"a number that is not a number", `[{"criterion":"two","status":"accept"},{"criterion":null,"status":"accept"}]`},
}

func TestGateRejectsAVerdictWhoseCriteriaCannotBeUsed(t *testing.T) {
	for _, tc := range unusableCriteria {
		t.Run(tc.name, func(t *testing.T) {
			f := newCritFixture(t, critOpts{plan: critPlan(critLines...),
				solo: `{"decision":"accept","notes":"NOTES","criteria":` + tc.entries + `}`})
			dec, rv := f.gate(t)
			if dec.Accept || rv.Accept {
				t.Fatalf("a top-level accept with no usable criterion entry must not accept: %+v", rv)
			}
			if !strings.Contains(rv.Notes, "AC1, AC2, AC3") {
				t.Errorf("notes %q should name every judged criterion", rv.Notes)
			}
			if len(rv.Criteria) != 3 {
				t.Fatalf("criteria = %+v, want the three judged recorded", rv.Criteria)
			}
			for n := 1; n <= 3; n++ {
				if e := entryOf(t, rv.Criteria, n); e.Status != verb.CriterionUnchecked || e.Text == "" {
					t.Errorf("entry %d = %+v, want unchecked with its text", n, e)
				}
			}
		})
	}
}

func TestPanelSeatRejectsAVerdictWhoseCriteriaCannotBeUsed(t *testing.T) {
	accAll := verdictJSON("accept", critEntry(1, "accept", ""), critEntry(2, "accept", ""), critEntry(3, "accept", ""))
	for _, tc := range unusableCriteria {
		t.Run(tc.name, func(t *testing.T) {
			f := newCritFixture(t, critOpts{panel: true, plan: critPlan(critLines...),
				s1: `{"decision":"accept","notes":"NOTES","criteria":` + tc.entries + `}`, s2: accAll})
			dec, rv := f.gate(t)
			s1, s2 := seatOf(t, rv, "s1"), seatOf(t, rv, "s2")
			if s1.Accept || !s2.Accept {
				t.Errorf("s1.Accept = %v, s2.Accept = %v, want only s2 to accept", s1.Accept, s2.Accept)
			}
			if dec.Accept {
				t.Errorf("a seat with no usable criterion entry must block the panel: %+v", rv)
			}
			if len(s1.Criteria) != 3 || entryOf(t, s1.Criteria, 1).Status != verb.CriterionUnchecked {
				t.Errorf("the seat record keeps its unchecked criteria: %+v", s1.Criteria)
			}
		})
	}
}

// The bundle path applies the same rule to each verdict of the bundle.
func TestBundleVerdictWhoseCriteriaCannotBeUsedDoesNotAccept(t *testing.T) {
	for _, tc := range unusableCriteria {
		t.Run(tc.name, func(t *testing.T) {
			out := `{"verdicts":[{"skill":"rev-a","decision":"accept","criteria":` + tc.entries + `}]}`
			decs, err := parseBundleDecisions([]byte(out), []string{"rev-a"})
			if err != nil || len(decs) != 1 {
				t.Fatalf("parse: %v %+v", err, decs)
			}
			d := decs[0]
			c := criteriaPrep{judged: agentartifact.NumberedCriteria(strings.Join(critLines, "\n"))}
			c.apply(&d)
			if d.Accept || len(d.Criteria) != 3 {
				t.Errorf("accept = %v, criteria = %+v", d.Accept, d.Criteria)
			}
		})
	}
}

// sty_48a86a38: an axis rubric judges the story as a whole, so a criteria[] that
// holds only the general entry is its whole verdict, not a skipped AC list.

const generalOnlyAccept = `{"decision":"accept","notes":"scope holds","criteria":[{"criterion":0,"status":"accept"}]}`

func generalOnlyReject(decision string) string {
	return `{"decision":"` + decision + `","notes":"NOTES","criteria":[{"criterion":0,"status":"reject","finding":"out of scope: X"}]}`
}

func TestGeneralOnlyVerdictDecidesForAWholeStoryRubric(t *testing.T) {
	check := func(t *testing.T, accept bool, criteria []verb.CriterionVerdict, notes string) {
		t.Helper()
		if !accept {
			t.Errorf("a lone general accept must accept: notes %q", notes)
		}
		if len(criteria) != 1 || criteria[0].Criterion != verb.GeneralCriterion || criteria[0].Status != verb.CriterionAccepted {
			t.Errorf("criteria = %+v, want only the general accept, no invented entries", criteria)
		}
		if strings.Contains(notes, "criteria not accepted") {
			t.Errorf("notes %q read as a rejection", notes)
		}
	}
	t.Run("solo", func(t *testing.T) {
		f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: generalOnlyAccept})
		dec, rv := f.gate(t)
		check(t, dec.Accept && rv.Accept, rv.Criteria, rv.Notes)
	})
	t.Run("panel seat", func(t *testing.T) {
		accAll := verdictJSON("accept", critEntry(1, "accept", ""), critEntry(2, "accept", ""), critEntry(3, "accept", ""))
		f := newCritFixture(t, critOpts{panel: true, plan: critPlan(critLines...), s1: generalOnlyAccept, s2: accAll})
		dec, rv := f.gate(t)
		s1 := seatOf(t, rv, "s1")
		check(t, s1.Accept && dec.Accept, s1.Criteria, s1.Notes)
	})
	t.Run("bundle", func(t *testing.T) {
		out := `{"verdicts":[{"skill":"rev-a","decision":"accept","notes":"scope holds","criteria":[{"criterion":0,"status":"accept"}]}]}`
		decs, err := parseBundleDecisions([]byte(out), []string{"rev-a"})
		if err != nil || len(decs) != 1 {
			t.Fatalf("parse: %v %+v", err, decs)
		}
		d := decs[0]
		criteriaPrep{judged: agentartifact.NumberedCriteria(strings.Join(critLines, "\n"))}.apply(&d)
		check(t, d.Accept, d.Criteria, d.Notes)
	})
}

func TestGeneralOnlyRejectNamesTheGeneralFinding(t *testing.T) {
	for _, decision := range []string{"accept", "reject"} {
		t.Run("top-level "+decision, func(t *testing.T) {
			f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: generalOnlyReject(decision)})
			dec, rv := f.gate(t)
			if dec.Accept || rv.Accept {
				t.Fatalf("a general reject must not accept: %+v", rv)
			}
			for _, want := range []string{"the general objection", "out of scope: X", "NOTES"} {
				if !strings.Contains(rv.Notes, want) {
					t.Errorf("notes %q should contain %q", rv.Notes, want)
				}
			}
			if strings.Contains(rv.Notes, "AC1") {
				t.Errorf("notes %q should not blame the numbered criteria", rv.Notes)
			}
			if len(rv.Criteria) != 1 {
				t.Errorf("criteria = %+v, want only the general entry", rv.Criteria)
			}
		})
	}
}

func TestGeneralOnlyVerdictRejectedWhenRubricRequiresCriteria(t *testing.T) {
	f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: generalOnlyAccept, requireCriteria: true})
	dec, rv := f.gate(t)
	if dec.Accept || rv.Accept {
		t.Fatalf("a rubric requiring per-criterion entries must not accept a lone general entry: %+v", rv)
	}
	for _, want := range []string{"criteria numbering did not match", "expected AC1–AC3, got 0", "scope holds"} {
		if !strings.Contains(rv.Notes, want) {
			t.Errorf("notes %q should contain %q", rv.Notes, want)
		}
	}
}

func TestRequiresPerCriterion(t *testing.T) {
	for _, tc := range []struct {
		name, rubric string
		want         bool
	}{
		{"required", "---\nname: r\nverdict_criteria: required\n---\nbody\n", true},
		{"quoted", "---\nname: r\nverdict_criteria: \"required\"\n---\nbody\n", true},
		{"another value", "---\nname: r\nverdict_criteria: optional\n---\nbody\n", false},
		{"absent", "---\nname: r\n---\nbody\n", false},
		{"no frontmatter", "verdict_criteria: required\n", false},
	} {
		if got := requiresPerCriterion(tc.rubric); got != tc.want {
			t.Errorf("%s: requiresPerCriterion = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGateRecordsAndStampsTheJudgedCriteria(t *testing.T) {
	// The model sends a text and an entry for a criterion that is not judged; the
	// binary keeps neither: it stamps the text from the plan section it judged.
	out := `{"decision":"accept","criteria":[
		{"criterion":1,"status":"accept","reviewed":"PREMISE-1 holds.","text":"model text"},
		{"criterion":2,"status":"accept"},{"criterion":3,"status":"accept"},{"criterion":9,"status":"reject"}]}`
	f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: out})
	_, rv := f.gate(t)
	if len(rv.Criteria) != 3 {
		t.Fatalf("criteria = %+v, want the three judged", rv.Criteria)
	}
	if e := entryOf(t, rv.Criteria, 1); e.Text != "first criterion" || e.Reviewed != "PREMISE-1 holds." {
		t.Errorf("entry 1 = %+v", e)
	}
	if _, _, source := f.solo.payload(t, 0); source != "doc:plan#Acceptance criteria" {
		t.Errorf("criteria_source = %q", source)
	}
}

// AC2: the plan section carries more criteria than the story, so a verdict that
// leaves the extra one out does not accept.
func TestGateJudgesThePlanSectionNotTheStory(t *testing.T) {
	seven := []string{"1. a", "2. b", "3. c", "4. d", "5. e", "6. f", "7. g"}
	var six []string
	for _, l := range seven[:6] {
		six = append(six, l)
	}
	entries := func(n int) []map[string]any {
		var es []map[string]any
		for i := 1; i <= n; i++ {
			es = append(es, critEntry(i, "accept", ""))
		}
		return es
	}
	f := newCritFixture(t, critOpts{plan: critPlan(seven...), storyAC: strings.Join(six, "\n"), solo: verdictJSON("accept", entries(6)...)})
	dec, rv := f.gate(t)
	if dec.Accept || !strings.Contains(rv.Notes, "AC7") {
		t.Errorf("a verdict that omits AC7 must not accept: %+v", rv)
	}
	f = newCritFixture(t, critOpts{plan: critPlan(seven...), storyAC: strings.Join(six, "\n"), solo: verdictJSON("accept", entries(7)...)})
	if dec, _ := f.gate(t); !dec.Accept {
		t.Errorf("all seven accepted: %+v", dec)
	}
}

func TestGateJudgesThePlanWhenTheStoryHasNoCriteria(t *testing.T) {
	f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: verdictJSON("accept",
		critEntry(1, "accept", ""), critEntry(2, "accept", ""), critEntry(3, "accept", ""))})
	_, rv := f.gate(t)
	if len(rv.Criteria) != 3 || entryOf(t, rv.Criteria, 3).Text != "third criterion" {
		t.Errorf("the plan's criteria are recorded: %+v", rv.Criteria)
	}
}

func TestGateJudgesTheStoryWhenNoSectionIsDeclared(t *testing.T) {
	f := newCritFixture(t, critOpts{
		planSkill: critPlanSkillNoSection, plan: critPlan("1. plan only"), storyAC: "1. story one\n2. story two",
		solo: verdictJSON("accept", critEntry(1, "accept", "")),
	})
	dec, rv := f.gate(t)
	if dec.Accept || !strings.Contains(rv.Notes, "AC2") {
		t.Errorf("the story's second criterion was not judged: %+v", rv)
	}
	if e := entryOf(t, rv.Criteria, 1); e.Text != "story one" {
		t.Errorf("entry 1 = %+v, want the story's text", e)
	}
	if _, _, source := f.solo.payload(t, 0); source != criteriaSourceStory {
		t.Errorf("criteria_source = %q", source)
	}
}

// ---- AC3: a panel seat decides from its criteria -----------------------------

func TestPanelSeatDecidesFromItsCriteria(t *testing.T) {
	accAll := verdictJSON("accept", critEntry(1, "accept", ""), critEntry(2, "accept", ""), critEntry(3, "accept", ""))
	f := newCritFixture(t, critOpts{
		panel: true, plan: critPlan(critLines...),
		s1: verdictJSON("accept", critEntry(1, "accept", ""), critEntry(2, "reject", "S1 AC2"), critEntry(3, "accept", "")),
		s2: accAll,
	})
	dec, rv := f.gate(t)
	if dec.Accept || rv.Accept {
		t.Fatalf("a seat that rejects AC2 must block the panel: %+v", rv)
	}
	s1, s2 := seatOf(t, rv, "s1"), seatOf(t, rv, "s2")
	if s1.Accept || !s2.Accept {
		t.Errorf("s1.Accept = %v, s2.Accept = %v", s1.Accept, s2.Accept)
	}
	if len(s1.Criteria) != 3 || entryOf(t, s1.Criteria, 2).Finding != "S1 AC2" {
		t.Errorf("the seat record keeps its criteria: %+v", s1.Criteria)
	}
	var fold struct {
		Seats []struct {
			Seat     string                  `json:"seat"`
			Accept   bool                    `json:"accept"`
			Criteria []verb.CriterionVerdict `json:"criteria"`
		} `json:"seats"`
		Combined []criterionCombine `json:"criteria_combined"`
	}
	if len(f.fold.payloads) != 1 {
		t.Fatalf("fold ran %d times", len(f.fold.payloads))
	}
	if err := json.Unmarshal([]byte(f.fold.payloads[0]), &fold); err != nil {
		t.Fatal(err)
	}
	if fold.Seats[0].Accept || len(fold.Seats[0].Criteria) != 3 {
		t.Errorf("the combine check sees s1's effective decision and criteria: %+v", fold.Seats[0])
	}
	if len(fold.Combined) != 3 || fold.Combined[1] != (criterionCombine{Criterion: 2, Accepts: 1, Rejects: 1}) {
		t.Errorf("criteria_combined = %+v", fold.Combined)
	}
}

func TestPanelOfVerdictsWithoutCriteriaFoldsAsBefore(t *testing.T) {
	f := newCritFixture(t, critOpts{panel: true, plan: critPlan(critLines...),
		s1: `{"decision":"accept","notes":"a"}`, s2: `{"decision":"accept","notes":"b"}`})
	dec, rv := f.gate(t)
	if !dec.Accept {
		t.Fatalf("both accept: %+v", rv)
	}
	if strings.Contains(f.fold.payloads[0], "criteria") {
		t.Errorf("a panel with no criteria sends the combine check nothing new: %s", f.fold.payloads[0])
	}
	if len(seatOf(t, rv, "s1").Criteria) != 0 {
		t.Errorf("seat records no criteria: %+v", rv.Seats)
	}
}

// ---- AC4: settled and open on a re-presentation ------------------------------

// settledPrior is a rejected round whose verdict accepted AC1-AC3, each resting on
// a premise quotation, against plan v1 (critLines).
func settledPrior() PriorVerdict {
	return PriorVerdict{
		Skill: "rev-a", Decision: "reject", Notes: "an objection", Attempt: 1,
		Criteria: []verb.CriterionVerdict{
			{Criterion: 1, Status: verb.CriterionAccepted, Reviewed: "PREMISE-1 holds.", Text: "first criterion"},
			{Criterion: 2, Status: verb.CriterionAccepted, Reviewed: "PREMISE-2 holds.", Text: "second criterion"},
			{Criterion: 3, Status: verb.CriterionAccepted, Reviewed: "PREMISE-3 holds.", Text: "third criterion"},
		},
	}
}

func editedPlan() string {
	return critPlan("1. first criterion", "2. second criterion, now reworded", "3. third criterion")
}

func TestRePresentationCarriesSettledCriteria(t *testing.T) {
	t.Run("the seat's only rejection is of a settled criterion", func(t *testing.T) {
		out := verdictJSON("reject", critEntry(1, "reject", "still objects to AC1"),
			critEntry(2, "accept", "reworded is fine"), critEntry(3, "accept", ""))
		f := newCritFixture(t, critOpts{carry: true, plan: editedPlan(), prior: []PriorVerdict{settledPrior()}, solo: out})
		dec, rv := f.gate(t)

		state, _, _ := f.solo.payload(t, 0)
		if len(state) != 3 || state[0].State != criterionSettled || state[1].State != criterionOpen || state[2].State != criterionSettled {
			t.Fatalf("only the edited criterion is open: %+v", state)
		}
		if state[1].Text != "second criterion, now reworded" || state[1].PriorStatus != verb.CriterionAccepted {
			t.Errorf("the open criterion shows its new text and the prior status: %+v", state[1])
		}
		if !dec.Accept || !rv.Accept {
			t.Fatalf("a rejection of a settled criterion must not hold the gate: %+v", rv)
		}
		if e := entryOf(t, rv.Criteria, 1); e.Status != verb.CriterionAccepted || e.CarriedFrom != 1 || e.Reviewed != "PREMISE-1 holds." {
			t.Errorf("AC1 is recorded as the carried prior accept: %+v", e)
		}
		if e := entryOf(t, rv.Criteria, 3); e.CarriedFrom != 1 {
			t.Errorf("AC3 is carried: %+v", e)
		}
		if e := entryOf(t, rv.Criteria, 2); e.CarriedFrom != 0 || e.Text != "second criterion, now reworded" {
			t.Errorf("AC2 was judged this round: %+v", e)
		}
	})

	t.Run("the seat rejects the open criterion", func(t *testing.T) {
		out := verdictJSON("reject", critEntry(1, "accept", ""), critEntry(2, "reject", "the rewording is wrong"), critEntry(3, "accept", ""))
		f := newCritFixture(t, critOpts{carry: true, plan: editedPlan(), prior: []PriorVerdict{settledPrior()}, solo: out})
		dec, rv := f.gate(t)
		if dec.Accept || rv.Accept {
			t.Fatalf("a rejected open criterion blocks the gate: %+v", rv)
		}
		_, failing := verb.CriteriaAccept(false, rv.Criteria, []int{1, 2, 3})
		if len(failing) != 1 || failing[0] != 2 {
			t.Errorf("the only failing criterion is AC2: %v in %+v", failing, rv.Criteria)
		}
	})

	t.Run("a seat that answers nothing about a settled criterion still has it carried", func(t *testing.T) {
		f := newCritFixture(t, critOpts{carry: true, plan: editedPlan(), prior: []PriorVerdict{settledPrior()},
			solo: verdictJSON("accept", critEntry(2, "accept", ""))})
		dec, rv := f.gate(t)
		if !dec.Accept || len(rv.Criteria) != 3 {
			t.Errorf("AC1 and AC3 are carried: %+v", rv)
		}
	})

	t.Run("a seat that sends no criteria[] has its settled criteria carried", func(t *testing.T) {
		const reject = `{"decision":"reject","notes":"AC1 is wrong"}`
		// Nothing open: the carried accepts decide, so the top-level reject of a
		// settled criterion does not hold the gate.
		f := newCritFixture(t, critOpts{carry: true, plan: critPlan(critLines...), prior: []PriorVerdict{settledPrior()}, solo: reject})
		dec, rv := f.gate(t)
		if !dec.Accept || !rv.Accept || len(rv.Criteria) != 3 {
			t.Fatalf("every criterion is settled, so the gate accepts: %+v", rv)
		}
		for n := 1; n <= 3; n++ {
			if e := entryOf(t, rv.Criteria, n); e.Status != verb.CriterionAccepted || e.CarriedFrom != 1 {
				t.Errorf("AC%d is the carried prior accept: %+v", n, e)
			}
		}
		// One criterion open: its carried siblings are recorded, but the seat said
		// nothing about the open one, so the top-level decision stands.
		f = newCritFixture(t, critOpts{carry: true, plan: editedPlan(), prior: []PriorVerdict{settledPrior()}, solo: reject})
		dec, rv = f.gate(t)
		if dec.Accept || rv.Accept {
			t.Errorf("an itemless reject with an open criterion still blocks: %+v", rv)
		}
		if len(rv.Criteria) != 2 || entryOf(t, rv.Criteria, 1).CarriedFrom != 1 || entryOf(t, rv.Criteria, 3).CarriedFrom != 1 {
			t.Errorf("AC1 and AC3 are recorded as carried: %+v", rv.Criteria)
		}
		f = newCritFixture(t, critOpts{carry: true, plan: editedPlan(), prior: []PriorVerdict{settledPrior()}, solo: `{"decision":"accept"}`})
		if dec, _ = f.gate(t); !dec.Accept {
			t.Error("a verdict with no criteria[] and a top-level accept is decided as before")
		}
		// The same for a panel seat.
		f = newCritFixture(t, critOpts{panel: true, carry: true, plan: critPlan(critLines...), prior: []PriorVerdict{settledPrior()},
			s1: reject, s2: reject})
		dec, rv = f.gate(t)
		if s1 := seatOf(t, rv, "s1"); !s1.Accept || len(s1.Criteria) != 3 || entryOf(t, s1.Criteria, 2).CarriedFrom != 1 {
			t.Errorf("the seat record carries its settled criteria and accepts: %+v", s1)
		}
	})

	t.Run("a quotation that left the material reopens its criterion", func(t *testing.T) {
		plan := strings.Replace(editedPlan(), "PREMISE-3 holds.", "PREMISE-3 was reworked.", 1)
		f := newCritFixture(t, critOpts{carry: true, plan: plan, prior: []PriorVerdict{settledPrior()},
			solo: verdictJSON("accept", critEntry(2, "accept", ""))})
		dec, _ := f.gate(t)
		state, _, _ := f.solo.payload(t, 0)
		if state[2].State != criterionOpen {
			t.Errorf("AC3's quotation is gone, so it is open: %+v", state)
		}
		if dec.Accept {
			t.Error("AC3 was not judged and not carried, so the gate does not accept")
		}
	})

	t.Run("a stale prior settles nothing", func(t *testing.T) {
		stale := settledPrior()
		stale.ReviewedStale = true
		for i := range stale.Criteria {
			stale.Criteria[i].Reviewed = ""
		}
		f := newCritFixture(t, critOpts{carry: true, plan: critPlan(critLines...), prior: []PriorVerdict{stale},
			solo: verdictJSON("accept", critEntry(1, "accept", ""), critEntry(2, "accept", ""), critEntry(3, "accept", ""))})
		f.gate(t)
		state, _, _ := f.solo.payload(t, 0)
		for _, s := range state {
			if s.State != criterionOpen {
				t.Errorf("every criterion is open on a stale prior: %+v", state)
			}
		}
	})

	t.Run("an edge that does not carry settles nothing", func(t *testing.T) {
		f := newCritFixture(t, critOpts{plan: editedPlan(), prior: []PriorVerdict{settledPrior()},
			solo: verdictJSON("reject", critEntry(1, "reject", "objects"), critEntry(2, "accept", ""), critEntry(3, "accept", ""))})
		dec, rv := f.gate(t)
		state, _, _ := f.solo.payload(t, 0)
		for _, s := range state {
			if s.State != criterionOpen {
				t.Errorf("no carry key, so every criterion is open: %+v", state)
			}
		}
		if dec.Accept || entryOf(t, rv.Criteria, 1).CarriedFrom != 0 {
			t.Errorf("nothing is carried without reviewer_carry: %+v", rv)
		}
	})
}

// A verdict that records criteria is not carried whole while a criterion beneath
// its unchanged quotation has changed.
func TestWholeVerdictCarryNeedsEveryCriterionUnchanged(t *testing.T) {
	accepted := settledPrior()
	accepted.Decision = "accept"
	accepted.Reviewed = "PREMISE-1 holds."
	other := rejected("rev-b")
	for _, tc := range []struct {
		name string
		plan string
		runs int
	}{
		{"unchanged", critPlan(critLines...), 0},
		{"a criterion changed", editedPlan(), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wf := spineWF("", "", "", "plan|planner|plan-skill|rev-a,rev-b|reviewer||"+carryKey, "done")
			docs := fakeDocs{workflow: wf, extraSkills: append(bundleSkillDocs("rev-a", "rev-b"),
				docindex.Doc{Kind: "skills", Name: "plan-skill", Body: critPlanSkill})}
			r := &bundleRunner{verdict: map[string]string{"rev-b": "reject"}}
			g := New(r, docs, "/repo", "")
			g.backoff = func(int) time.Duration { return 0 }
			g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
				return config.AgentBinding{Command: "planner-fake -p {system}", Tools: "Read"}, name == "planner"
			})
			g.SetDocsResolver(func(context.Context, string) []DocState {
				return []DocState{{Name: "plan", Type: "plan", Body: tc.plan}}
			})
			g.SetPriorVerdictsResolver(func(context.Context, string, string, string, verb.StaleExemption) []PriorVerdict {
				return []PriorVerdict{accepted, other}
			})
			if _, err := g.Gate(context.Background(), workitem.Item{ID: "sty_c", Status: "backlog", Category: "feature", Title: "T", Body: "B"}, "plan"); err != nil {
				t.Fatal(err)
			}
			if got := r.sessionsFor("rev-a"); got < tc.runs {
				t.Errorf("rev-a judged %d times, want at least %d", got, tc.runs)
			}
			if tc.runs == 0 && r.sessionsFor("rev-a") != 0 {
				t.Errorf("an unchanged accept is carried, not dispatched: %d", r.sessionsFor("rev-a"))
			}
		})
	}
}

// A panel seat is carried whole only while its criteria are unchanged too.
func TestPanelSeatCarryNeedsEveryCriterionUnchanged(t *testing.T) {
	seats := func() []verb.PanelSeat {
		return []verb.PanelSeat{
			{Seat: "s1", Accept: true, Reviewed: "PREMISE-1 holds.", Attempt: 1, Criteria: settledPrior().Criteria},
			{Seat: "s2", Accept: false, Reviewed: "PREMISE-2 holds.", Notes: "objection", Attempt: 1},
		}
	}
	row := PriorVerdict{Skill: "rev-a", Decision: "reject", Seats: seats()}
	acc := verdictJSON("accept", critEntry(1, "accept", ""), critEntry(2, "accept", ""), critEntry(3, "accept", ""))

	f := newCritFixture(t, critOpts{panel: true, carry: true, plan: critPlan(critLines...), prior: []PriorVerdict{row}, s1: acc, s2: `{"decision":"reject"}`})
	_, rv := f.gate(t)
	if f.s1.calls() != 0 || seatOf(t, rv, "s1").CarriedFrom != 1 {
		t.Errorf("an unchanged seat accept is carried: s1 ran %d, %+v", f.s1.calls(), seatOf(t, rv, "s1"))
	}
	if got := seatOf(t, rv, "s1").Criteria; len(got) != 3 || got[0].CarriedFrom != 1 {
		t.Errorf("a carried seat keeps its criteria, marked carried: %+v", got)
	}

	f = newCritFixture(t, critOpts{panel: true, carry: true, plan: editedPlan(), prior: []PriorVerdict{row}, s1: acc, s2: `{"decision":"reject"}`})
	f.gate(t)
	if f.s1.calls() != 1 {
		t.Errorf("a seat whose criterion changed is judged: ran %d", f.s1.calls())
	}
	state, _, _ := f.s1.payload(t, 0)
	if len(state) != 3 || state[1].State != criterionOpen || state[0].State != criterionSettled {
		t.Errorf("s1 sees AC2 open and AC1 settled: %+v", state)
	}
}

// ---- AC5: the other seats' open findings ------------------------------------

func TestPanelSeatSeesPeerFindingsNeverItsOwn(t *testing.T) {
	row := PriorVerdict{Skill: "rev-a", Decision: "reject", Attempt: 1, Seats: []verb.PanelSeat{
		{Seat: "s1", Accept: false, Attempt: 1, Criteria: []verb.CriterionVerdict{
			{Criterion: 1, Status: verb.CriterionRejected, Finding: "S1-FINDING-AC1"},
			{Criterion: 2, Status: verb.CriterionAccepted, Finding: "S1-ACCEPTED-AC2"},
		}},
		{Seat: "s2", Accept: false, Attempt: 1, Criteria: []verb.CriterionVerdict{
			{Criterion: 3, Status: verb.CriterionUnchecked, Finding: "S2-FINDING-AC3"},
			{Criterion: 2, Status: verb.CriterionRejected, Finding: "S2-FINDING-AC2"},
		}},
	}}
	acc := verdictJSON("accept", critEntry(1, "accept", ""), critEntry(2, "accept", ""), critEntry(3, "accept", ""))
	f := newCritFixture(t, critOpts{panel: true, plan: critPlan(critLines...), prior: []PriorVerdict{row}, s1: acc, s2: acc})
	f.gate(t)

	_, peers1, _ := f.s1.payload(t, 0)
	_, peers2, _ := f.s2.payload(t, 0)
	want1 := []peerFinding{
		{Seat: "s2", Criterion: 3, Status: "unchecked", Finding: "S2-FINDING-AC3"},
		{Seat: "s2", Criterion: 2, Status: "reject", Finding: "S2-FINDING-AC2"},
	}
	want2 := []peerFinding{{Seat: "s1", Criterion: 1, Status: "reject", Finding: "S1-FINDING-AC1"}}
	if fmt.Sprint(peers1) != fmt.Sprint(want1) {
		t.Errorf("s1 peer findings = %+v, want %+v", peers1, want1)
	}
	if fmt.Sprint(peers2) != fmt.Sprint(want2) {
		t.Errorf("s2 peer findings = %+v, want %+v", peers2, want2)
	}
	for _, r := range []*scriptRunner{f.s1, f.s2} {
		own := map[*scriptRunner]string{f.s1: "S1-", f.s2: "S2-"}[r]
		_, peers, _ := r.payload(t, 0)
		for _, p := range peers {
			if strings.Contains(p.Finding, own) || p.Seat == strings.ToLower(strings.TrimSuffix(own, "-")) {
				t.Errorf("a seat is never shown its own finding: %+v", p)
			}
		}
	}
}

func TestPeerFindingsForARejectingPeerWithoutCriteria(t *testing.T) {
	prior := []PriorVerdict{{Skill: "rev-a", Seats: []verb.PanelSeat{
		{Seat: "s1", Accept: true},
		{Seat: "s2", Accept: false, Notes: "prose objection"},
		{Seat: "s3", Unavailable: true, Notes: "timeout"},
	}}}
	got := peerFindingsFor(prior, "rev-a", "s1")
	if len(got) != 1 || got[0] != (peerFinding{Seat: "s2", Criterion: 0, Status: "reject", Finding: "prose objection"}) {
		t.Errorf("peer findings = %+v", got)
	}
	if got := peerFindingsFor(prior, "other", "s1"); got != nil {
		t.Errorf("another skill's rows are not this panel's: %+v", got)
	}
}

// AC7: with no earlier verdict that recorded criteria, and no reviewer_carry, the
// work item a reviewer opens gains no criteria_state or peer_findings.
func TestPayloadUnchangedWhenNothingRecordedCriteria(t *testing.T) {
	f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: `{"decision":"accept"}`})
	f.gate(t)
	state, peers, _ := f.solo.payload(t, 0)
	if state != nil || peers != nil {
		t.Errorf("criteria_state = %+v, peer_findings = %+v, want neither", state, peers)
	}
}

// Criterion numbers on the wire survive the odd forms models send.
func TestCriterionNumberForms(t *testing.T) {
	var got []rawCriterion
	if err := json.Unmarshal([]byte(`[{"criterion":2},{"criterion":"3"},{"criterion":"ac4"},{"criterion":2.5},{"criterion":null},{"criterion":[1]}]`), &got); err != nil {
		t.Fatal(err)
	}
	want := []criterionNo{2, 3, 4, -1, -1, -1}
	for i, w := range want {
		if got[i].Criterion != w {
			t.Errorf("form %d = %d, want %d", i, got[i].Criterion, w)
		}
	}
}

// ---- sty_3b789fa5: a criterion unmet while the behavior holds -----------------

func TestParseDecisionKeepsUnmet(t *testing.T) {
	dec, err := parseDecision([]byte(`{"decision":"accept","criteria":[{"criterion":0,"status":"accept"},{"criterion":2,"status":" Unmet ","finding":"proof could only be captured earlier"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(dec.Criteria) != 2 || dec.Criteria[1].Status != verb.CriterionUnmet || dec.Criteria[1].Finding != "proof could only be captured earlier" {
		t.Errorf("criteria = %+v, want AC2 recorded unmet with its finding", dec.Criteria)
	}
}

// A recorded verdict shaped like sty_28665836's release → done: six criteria, the
// behavior on the card holds, and AC6's proof could only have been captured
// earlier. It accepts with AC6 recorded unmet, named in the notes.
func TestGateAcceptsAnUnmetCriterionWhenTheBehaviorHolds(t *testing.T) {
	six := []string{"1. one", "2. two", "3. three", "4. four", "5. five", "6. six"}
	entries := []map[string]any{critEntry(0, "accept", "behavior holds: no FAIL or warning was introduced")}
	for n := 1; n <= 5; n++ {
		entries = append(entries, critEntry(n, "accept", "met"))
	}
	entries = append(entries, critEntry(6, "unmet", "the proof could only have been captured before the change"))
	f := newCritFixture(t, critOpts{plan: critPlan(six...), solo: verdictJSON("accept", entries...)})
	dec, rv := f.gate(t)
	if !dec.Accept || !rv.Accept {
		t.Fatalf("a behavior accept with AC6 unmet must accept: %+v", rv)
	}
	if e := entryOf(t, rv.Criteria, 6); e.Status != verb.CriterionUnmet || e.Text != "six" || e.Finding == "" {
		t.Errorf("AC6 must stay in the record as unmet with its text and finding: %+v", e)
	}
	if e := entryOf(t, rv.Criteria, 0); e.Status != verb.CriterionAccepted {
		t.Errorf("the behavior accept must stay in the record: %+v", e)
	}
	for _, want := range []string{"unmet", "AC6", "NOTES"} {
		if !strings.Contains(rv.Notes, want) {
			t.Errorf("notes %q should contain %q", rv.Notes, want)
		}
	}
}

func TestGateRejectsAnUnmetCriterionWithoutABehaviorAccept(t *testing.T) {
	acc := func(n int) map[string]any { return critEntry(n, "accept", "ok") }
	unmet := critEntry(3, "unmet", "cannot be met")
	cases := []struct {
		name    string
		out     string
		failing string
	}{
		{"no general entry", verdictJSON("accept", acc(1), acc(2), unmet), "AC3"},
		{"a general reject", verdictJSON("accept", critEntry(0, "reject", "the behavior does not hold"), acc(1), acc(2), unmet), "the general objection, AC3"},
		{"another criterion rejected", verdictJSON("accept", acc(0), critEntry(1, "reject", "bad"), acc(2), unmet), "AC1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCritFixture(t, critOpts{plan: critPlan(critLines...), solo: tc.out})
			dec, rv := f.gate(t)
			if dec.Accept || rv.Accept {
				t.Fatalf("must not accept: %+v", rv)
			}
			if !strings.Contains(rv.Notes, "criteria not accepted: "+tc.failing) {
				t.Errorf("notes %q should name %q", rv.Notes, tc.failing)
			}
		})
	}
}

func TestCombineCriteriaTalliesUnmetApart(t *testing.T) {
	got := combineCriteria([][]verb.CriterionVerdict{
		{{Criterion: 1, Status: verb.CriterionUnmet}, {Criterion: 2, Status: verb.CriterionUnchecked}},
		{{Criterion: 1, Status: verb.CriterionAccepted}, {Criterion: 2, Status: verb.CriterionUnmet}},
	})
	want := []criterionCombine{
		{Criterion: 1, Accepts: 1, Unmet: 1},
		{Criterion: 2, Unchecked: 1, Unmet: 1},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("criteria_combined = %+v, want %+v", got, want)
	}
}
