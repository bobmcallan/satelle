package agentstep

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_da9e8d0a: a panel seat's verdict keeps its own reviewed quotation and
// attempt, a re-presented edge hands each seat its own prior verdict, and under
// reviewer_carry an unchanged seat accept is carried while the combine check
// still judges the whole panel.

const panelFoldSkill = "---\nname: fold\ntype: skill\ndescription: panel fold\ncheck: \"run-fold\"\n---\n# fold\nFold.\n"

// seatRunner answers as one panel seat and snapshots every work item it opens.
type seatRunner struct {
	mu       sync.Mutex
	command  string
	decision string
	reviewed string
	shots    []boundShot
}

func (r *seatRunner) Name() string    { return "seat-fake" }
func (r *seatRunner) Command() string { return r.command }
func (r *seatRunner) Run(_ context.Context, req agentcli.Request) ([]byte, error) {
	r.mu.Lock()
	r.shots = append(r.shots, boundShot{payload: req.Payload, opened: openPayloadFiles(req.Payload)})
	r.mu.Unlock()
	out, _ := json.Marshal(map[string]string{"decision": r.decision, "notes": "from " + r.command, "reviewed": r.reviewed})
	return out, nil
}

func (r *seatRunner) invocations() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.shots)
}

// foldCalls records each combine-check stdin and accepts only when every seat
// accepted and none was unavailable.
type foldCalls struct {
	mu       sync.Mutex
	payloads []struct {
		Seats []struct {
			Seat        string `json:"seat"`
			Accept      bool   `json:"accept"`
			Unavailable bool   `json:"unavailable"`
		} `json:"seats"`
		Declared int `json:"declared"`
	}
}

func (f *foldCalls) check(_ context.Context, _, _, payload string) (string, error) {
	var p struct {
		Seats []struct {
			Seat        string `json:"seat"`
			Accept      bool   `json:"accept"`
			Unavailable bool   `json:"unavailable"`
		} `json:"seats"`
		Declared int `json:"declared"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return "", err
	}
	f.mu.Lock()
	f.payloads = append(f.payloads, p)
	f.mu.Unlock()
	for _, s := range p.Seats {
		if !s.Accept || s.Unavailable {
			return "a seat did not accept\n", errFakeExit
		}
	}
	return "ok\n", nil
}

// panelFixture is a one-skill edge (rev-a) judged by seats s1 and s2.
type panelFixture struct {
	g     *Engine
	s1    *seatRunner
	s2    *seatRunner
	fold  *foldCalls
	plan  *string
	prior []PriorVerdict
}

// seatSpec is how a fixture seat answers.
type seatSpec struct{ decision, reviewed string }

func newPanelFixture(t *testing.T, carry bool, prior []PriorVerdict, spec1, spec2 seatSpec) *panelFixture {
	t.Helper()
	s1 := &seatRunner{command: "seat-s1 -p {system}", decision: spec1.decision, reviewed: spec1.reviewed}
	s2 := &seatRunner{command: "seat-s2 -p {system}", decision: spec2.decision, reviewed: spec2.reviewed}
	extra := "panel = [\"s1\", \"s2\"]\ncombine = \"fold\""
	if carry {
		extra += "\n" + carryKey
	}
	wf := spineWF("", "", "", "in_progress|executor||rev-a|||"+extra, "done")
	skills := append(bundleSkillDocs("rev-a"), docindex.Doc{Kind: "skills", Name: "fold", Body: panelFoldSkill})
	g := New(&bundleRunner{}, fakeDocs{workflow: wf, extraSkills: skills}, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	f := &panelFixture{g: g, s1: s1, s2: s2, fold: &foldCalls{}, prior: prior}
	plan := carryPlan
	f.plan = &plan
	g.check = f.fold.check
	g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
		switch name {
		case "s1":
			return config.AgentBinding{Command: s1.command, Role: config.RoleReviewer, Tools: "Read"}, true
		case "s2":
			return config.AgentBinding{Command: s2.command, Role: config.RoleReviewer, Tools: "Read"}, true
		}
		return config.AgentBinding{}, false
	})
	g.newRunner = func(_, command string) (agentcli.Runner, error) {
		if strings.HasPrefix(command, "seat-s1") {
			return s1, nil
		}
		return s2, nil
	}
	g.SetDocsResolver(func(context.Context, string) []DocState {
		return []DocState{{Name: "plan", Type: "plan", Body: *f.plan}}
	})
	g.SetPriorVerdictsResolver(func(context.Context, string, string, string, verb.StaleExemption) []PriorVerdict {
		return append([]PriorVerdict(nil), f.prior...)
	})
	return f
}

func (f *panelFixture) gate(t *testing.T) verb.ReviewerVerdict {
	t.Helper()
	dec, err := f.g.Gate(context.Background(), workitem.Item{ID: "sty_p", Status: "backlog", Category: "feature", Title: "T", Body: "B"}, "in_progress")
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	return reviewerOf(t, dec, "rev-a")
}

func seatOf(t *testing.T, rv verb.ReviewerVerdict, seat string) verb.PanelSeat {
	t.Helper()
	for _, s := range rv.Seats {
		if s.Seat == seat {
			return s
		}
	}
	t.Fatalf("no seat %s in %+v", seat, rv.Seats)
	return verb.PanelSeat{}
}

// panelRow is the combined row a prior round recorded.
func panelRow(seats ...verb.PanelSeat) PriorVerdict {
	return PriorVerdict{Skill: "rev-a", Decision: "reject", Notes: "COMBINED-NOTES", Seats: seats}
}

// AC1: a combine row records each seat's quotation, truncation flag and attempt.
func TestPanelRowRecordsSeatReviewedAndAttempt(t *testing.T) {
	prior := []PriorVerdict{rejected("other"), rejected("other")}
	f := newPanelFixture(t, false, prior,
		seatSpec{decision: "accept", reviewed: "PLAN-A"},
		seatSpec{decision: "reject", reviewed: strings.Repeat("x", reviewedCeiling+1)})
	rv := f.gate(t)
	s1, s2 := seatOf(t, rv, "s1"), seatOf(t, rv, "s2")
	if s1.Reviewed != "PLAN-A" || s1.ReviewedTruncated || !s1.Accept {
		t.Errorf("s1 = %+v, want the accept with its quotation", s1)
	}
	if s2.Reviewed != "" || !s2.ReviewedTruncated || s2.Accept {
		t.Errorf("s2 = %+v, want an over-ceiling quotation dropped and flagged", s2)
	}
	if s1.Attempt != 3 || s2.Attempt != 3 {
		t.Errorf("attempts = %d, %d, want the edge's next verdict number 3", s1.Attempt, s2.Attempt)
	}
	if s1.CarriedFrom != 0 || s2.CarriedFrom != 0 {
		t.Errorf("a judged seat carries nothing: %+v %+v", s1, s2)
	}
}

// seatPriors decodes the prior verdicts a seat opened.
func seatPriors(t *testing.T, r *seatRunner) []PriorVerdict {
	t.Helper()
	if len(r.shots) != 1 {
		t.Fatalf("seat opened %d work items, want 1", len(r.shots))
	}
	var got []PriorVerdict
	if err := json.Unmarshal(openedAt(t, r.shots[0].opened, r.shots[0].payload, "prior_verdicts"), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// AC2: on a re-presentation each seat opens its own latest verdict, with its own
// quotation — not the combined row's and not another seat's.
func TestPanelSeatPriorVerdictsAreItsOwn(t *testing.T) {
	prior := []PriorVerdict{
		rejected("other"),
		panelRow(
			verb.PanelSeat{Seat: "s1", Accept: true, Reviewed: "Q1", Notes: "S1-NOTES", Attempt: 2},
			verb.PanelSeat{Seat: "s2", Accept: false, Reviewed: "Q2", Notes: "S2-NOTES", Attempt: 2}),
	}
	f := newPanelFixture(t, false, prior, seatSpec{decision: "accept"}, seatSpec{decision: "accept"})
	f.gate(t)
	for _, tc := range []struct {
		r                         *seatRunner
		decision, reviewed, notes string
		other                     string
	}{
		{f.s1, "accept", "Q1", "S1-NOTES", "Q2"},
		{f.s2, "reject", "Q2", "S2-NOTES", "Q1"},
	} {
		got := seatPriors(t, tc.r)
		if len(got) != 2 || got[0].Skill != "other" {
			t.Fatalf("%s priors = %+v, want the other skill's row then its own", tc.r.command, got)
		}
		own := got[1]
		if own.Skill != "rev-a" || own.Decision != tc.decision || own.Reviewed != tc.reviewed || own.Notes != tc.notes || own.Attempt != 2 {
			t.Errorf("%s own prior = %+v", tc.r.command, own)
		}
		raw, _ := json.Marshal(got)
		for _, leak := range []string{"COMBINED-NOTES", tc.other} {
			if strings.Contains(string(raw), leak) {
				t.Errorf("%s priors leak %q: %s", tc.r.command, leak, raw)
			}
		}
	}
}

// AC3: under until_accept an unchanged seat accept is carried, only the changed
// seat is re-run, and the combine check still sees every declared seat.
func TestPanelSeatCarry(t *testing.T) {
	prior := func() []PriorVerdict {
		return []PriorVerdict{panelRow(
			verb.PanelSeat{Seat: "s1", Accept: true, Reviewed: "PLAN-A", Attempt: 1},
			verb.PanelSeat{Seat: "s2", Accept: false, Reviewed: "PLAN-B", Notes: "objection", Attempt: 1})}
	}

	t.Run("the other seat rejects again", func(t *testing.T) {
		f := newPanelFixture(t, true, prior(), seatSpec{decision: "accept"}, seatSpec{decision: "reject"})
		rv := f.gate(t)
		if rv.Accept {
			t.Fatalf("a rejecting seat must block the panel: %+v", rv)
		}
		if f.s1.invocations() != 0 || f.s2.invocations() != 1 {
			t.Errorf("s1 = %d, s2 = %d invocations, want 0 and 1", f.s1.invocations(), f.s2.invocations())
		}
		if s1 := seatOf(t, rv, "s1"); s1.CarriedFrom != 1 || !s1.Accept || s1.Reviewed != "PLAN-A" || s1.Attempt != 2 {
			t.Errorf("s1 = %+v, want a carried accept keeping its quotation", s1)
		}
		if len(f.fold.payloads) != 1 || len(f.fold.payloads[0].Seats) != 2 || f.fold.payloads[0].Declared != 2 {
			t.Errorf("the combine check must judge the full panel once: %+v", f.fold.payloads)
		}
	})

	t.Run("the other seat accepts so the carried seat is dispatched fresh", func(t *testing.T) {
		f := newPanelFixture(t, true, prior(), seatSpec{decision: "accept", reviewed: "PLAN-A"}, seatSpec{decision: "accept"})
		rv := f.gate(t)
		if !rv.Accept {
			t.Fatalf("both seats accept: %+v", rv)
		}
		if f.s1.invocations() != 1 || f.s2.invocations() != 1 {
			t.Errorf("s1 = %d, s2 = %d invocations, want 1 each", f.s1.invocations(), f.s2.invocations())
		}
		for _, s := range rv.Seats {
			if s.CarriedFrom != 0 || !s.Accept {
				t.Errorf("the advancing verdict must hold a fresh accept from every seat: %+v", rv.Seats)
			}
		}
		if len(f.fold.payloads) != 2 {
			t.Errorf("fold ran %d times, want the carried pass and the confirming pass", len(f.fold.payloads))
		}
	})

	t.Run("the quotation changed", func(t *testing.T) {
		f := newPanelFixture(t, true, prior(), seatSpec{decision: "accept"}, seatSpec{decision: "reject"})
		*f.plan = "a plan that no longer quotes it"
		rv := f.gate(t)
		if f.s1.invocations() != 1 || seatOf(t, rv, "s1").CarriedFrom != 0 {
			t.Errorf("s1 must be judged when its quotation left the material: %d %+v", f.s1.invocations(), rv.Seats)
		}
	})

	t.Run("no carry key", func(t *testing.T) {
		f := newPanelFixture(t, false, prior(), seatSpec{decision: "accept"}, seatSpec{decision: "reject"})
		f.gate(t)
		if f.s1.invocations() != 1 || f.s2.invocations() != 1 {
			t.Errorf("s1 = %d, s2 = %d invocations, want both dispatched", f.s1.invocations(), f.s2.invocations())
		}
	})

	t.Run("every seat would carry", func(t *testing.T) {
		both := []PriorVerdict{panelRow(
			verb.PanelSeat{Seat: "s1", Accept: true, Reviewed: "PLAN-A", Attempt: 1},
			verb.PanelSeat{Seat: "s2", Accept: true, Reviewed: "PLAN-B", Attempt: 1})}
		f := newPanelFixture(t, true, both, seatSpec{decision: "accept"}, seatSpec{decision: "accept"})
		f.gate(t)
		if f.s1.invocations() != 1 || f.s2.invocations() != 1 {
			t.Errorf("carry is moot when no seat is dispatched: %d %d", f.s1.invocations(), f.s2.invocations())
		}
	})
}

// AC4: a seat that was unavailable has no quotation and is judged in full.
func TestPanelSeatUnavailablePriorIsJudgedInFull(t *testing.T) {
	prior := []PriorVerdict{
		panelRow(
			verb.PanelSeat{Seat: "s1", Accept: true, Reviewed: "PLAN-A"},
			verb.PanelSeat{Seat: "s2", Accept: true, Reviewed: "PLAN-B"}),
		panelRow(
			verb.PanelSeat{Seat: "s1", Accept: true, Reviewed: "PLAN-A"},
			verb.PanelSeat{Seat: "s2", Unavailable: true, Notes: "timeout"}),
	}
	for i := range prior {
		prior[i].Attempt = i + 1
	}
	pv, ok := latestSeatEntry(prior, "rev-a", "s2")
	if !ok || !pv.Unavailable || pv.Decision != "reject" || pv.Reviewed != "" || pv.Notes != "timeout" {
		t.Fatalf("latest s2 entry = %+v, want an unavailable reject with no quotation", pv)
	}
	if quotationCarries(pv, carryPlan) {
		t.Error("an unavailable seat must never carry")
	}

	f := newPanelFixture(t, true, prior, seatSpec{decision: "accept"}, seatSpec{decision: "reject"})
	rv := f.gate(t)
	if f.s1.invocations() != 0 || f.s2.invocations() != 1 {
		t.Errorf("s1 = %d, s2 = %d invocations, want s1 carried and s2 judged", f.s1.invocations(), f.s2.invocations())
	}
	if got := seatPriors(t, f.s2); len(got) != 2 || !got[1].Unavailable || got[1].Reviewed != "" {
		t.Errorf("s2 opened %+v, want its unavailable round with no quotation", got)
	}
	if s2 := seatOf(t, rv, "s2"); s2.CarriedFrom != 0 {
		t.Errorf("s2 = %+v", s2)
	}
}

// A seat the latest row does not record is judged in full: an older round's
// accept is never carried past the latest row's staleness check.
func TestPanelSeatMissingFromLatestRowIsNotCarried(t *testing.T) {
	prior := []PriorVerdict{
		{Skill: "rev-a", Decision: "reject", Attempt: 1, Seats: []verb.PanelSeat{{Seat: "s1", Accept: true, Reviewed: "PLAN-A"}}},
		{Skill: "rev-a", Decision: "reject", Attempt: 2, Seats: []verb.PanelSeat{{Seat: "s2", Accept: false}}},
	}
	if pv, ok := latestSeatEntry(prior, "rev-a", "s1"); ok {
		t.Errorf("s1 is not on the latest row, got %+v", pv)
	}
}

// A stale latest row keeps its staleness marks on each seat's own entry.
func TestSeatPriorVerdictsKeepStaleMarks(t *testing.T) {
	all := []PriorVerdict{{
		Skill: "rev-a", Decision: "reject", Attempt: 1, ReviewedStale: true,
		EvidenceSince: []verb.EvidenceSince{{Kind: "story_doc_attached"}},
		Seats:         []verb.PanelSeat{{Seat: "s1", Accept: true}},
	}}
	got := seatPriorVerdicts(all, "rev-a", "s1")
	if len(got) != 1 || !got[0].ReviewedStale || len(got[0].EvidenceSince) != 1 || quotationCarries(got[0], carryPlan) {
		t.Fatalf("own entry = %+v, want the parent row's staleness and no carry", got)
	}
}

func TestQuotationCarries(t *testing.T) {
	const judged = "intro PLAN-A outro"
	for _, tc := range []struct {
		name string
		pv   PriorVerdict
		want bool
	}{
		{"accept, present", PriorVerdict{Decision: "accept", Reviewed: "PLAN-A"}, true},
		{"reject", PriorVerdict{Decision: "reject", Reviewed: "PLAN-A"}, false},
		{"stale", PriorVerdict{Decision: "accept", Reviewed: "PLAN-A", ReviewedStale: true}, false},
		{"truncated", PriorVerdict{Decision: "accept", Reviewed: "PLAN-A", ReviewedTruncated: true}, false},
		{"empty", PriorVerdict{Decision: "accept"}, false},
		{"blank", PriorVerdict{Decision: "accept", Reviewed: "  \n"}, false},
		{"absent", PriorVerdict{Decision: "accept", Reviewed: "PLAN-Z"}, false},
		{"unavailable seat", seatPriorVerdicts([]PriorVerdict{{Skill: "s", Seats: []verb.PanelSeat{{Seat: "x", Unavailable: true, Reviewed: "PLAN-A"}}}}, "s", "x")[0], false},
	} {
		if got := quotationCarries(tc.pv, judged); got != tc.want {
			t.Errorf("%s: quotationCarries = %v, want %v", tc.name, got, tc.want)
		}
	}
}
