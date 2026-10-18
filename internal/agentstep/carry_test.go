package agentstep

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// sty_59720b8b: a step that declares reviewer_carry = "until_accept" leaves a
// reviewer whose earlier accept still quotes the current material undispatched
// while another reviewer is dispatched, and re-dispatches every carried reviewer
// in the round that would advance the edge.

const carryKey = `reviewer_carry = "until_accept"`

// carryPlan is the performer document every fixture judges; the quotations in
// the prior verdicts are substrings of it.
const carryPlan = "intro PLAN-A middle PLAN-B and PLAN-C outro"

// sessionsFor counts the sessions that judged skill: a bundled one that carries
// its rubric heading, or a lone one whose system prompt opens with the skill.
func (r *bundleRunner) sessionsFor(skill string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, req := range r.reqs {
		matches := bundleHeading.FindAllStringSubmatch(req.SystemPrompt, -1)
		if len(matches) == 0 {
			if strings.Contains(req.SystemPrompt, "# "+skill+"\n") {
				n++
			}
			continue
		}
		for _, m := range matches {
			if m[1] == skill {
				n++
			}
		}
	}
	return n
}

// carrySchedulers are the three ways an edge schedules its reviewers.
var carrySchedulers = []struct{ name, par, extra string }{
	{"serial", "0", ""},
	{"parallel", "2", ""},
	{"bundle", "", "bundle = true\n"},
}

func accepted(skill, reviewed string) PriorVerdict {
	return PriorVerdict{Skill: skill, Decision: "accept", Reviewed: reviewed}
}

func rejected(skill string) PriorVerdict {
	return PriorVerdict{Skill: skill, Decision: "reject", Notes: "objection"}
}

// carryEngine builds an engine over a three-reviewer edge (rev-a, rev-b, rev-c)
// that declares reviewer_carry, answering from prior and judging docs from
// carryPlan. Every dispatched reviewer answers per verdict (default accept).
func carryEngine(t *testing.T, par, extra string, prior []PriorVerdict, verdict map[string]string) (*Engine, *bundleRunner) {
	t.Helper()
	wf := spineWF("", "", "", "in_progress|executor||rev-a,rev-b,rev-c|reviewer|"+par+"|"+extra+carryKey, "done")
	docs := fakeDocs{workflow: wf, extraSkills: bundleSkillDocs("rev-a", "rev-b", "rev-c")}
	r := &bundleRunner{verdict: verdict}
	g := New(r, docs, "/repo", "")
	g.backoff = func(int) time.Duration { return 0 }
	g.SetDocsResolver(func(context.Context, string) []DocState {
		return []DocState{{Name: "plan", Type: "plan", Body: carryPlan}}
	})
	g.SetPriorVerdictsResolver(func(context.Context, string, string, string, verb.StaleExemption) []PriorVerdict {
		return append([]PriorVerdict(nil), prior...)
	})
	return g, r
}

func carryGate(t *testing.T, g *Engine) verb.GateDecision {
	t.Helper()
	dec, err := g.Gate(context.Background(), workitem.Item{ID: "sty_c", Status: "backlog", Category: "feature", Title: "T", Body: "B"}, "in_progress")
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	return dec
}

func reviewerOf(t *testing.T, dec verb.GateDecision, skill string) verb.ReviewerVerdict {
	t.Helper()
	for _, rv := range dec.Reviewers {
		if rv.Skill == skill {
			return rv
		}
	}
	t.Fatalf("no verdict for %s in %+v", skill, dec.Reviewers)
	return verb.ReviewerVerdict{}
}

// AC3 + AC7: an accept with a verbatim quotation is not dispatched while another
// reviewer is, and is recorded as a carried accept — under every scheduler.
func TestCarry_AcceptWithUnchangedQuotationIsNotDispatched(t *testing.T) {
	prior := []PriorVerdict{rejected("rev-a"), accepted("rev-b", "PLAN-B"), rejected("rev-c")}
	for _, sc := range carrySchedulers {
		t.Run(sc.name, func(t *testing.T) {
			g, r := carryEngine(t, sc.par, sc.extra, prior, map[string]string{"rev-a": "reject"})
			dec := carryGate(t, g)
			if dec.Accept {
				t.Fatalf("a rejecting reviewer must block the edge: %+v", dec)
			}
			if n := r.sessionsFor("rev-b"); n != 0 {
				t.Errorf("rev-b was dispatched %d time(s), want 0", n)
			}
			if r.sessionsFor("rev-a") != 1 {
				t.Errorf("rev-a must be dispatched once, got %d", r.sessionsFor("rev-a"))
			}
			b := reviewerOf(t, dec, "rev-b")
			if !b.Accept || b.CarriedFrom != 2 || b.Reviewed != "PLAN-B" || b.Command != "" || b.Order != 1 {
				t.Errorf("rev-b verdict = %+v, want a carried accept from attempt 2 with no command", b)
			}
			if !strings.Contains(b.Notes, "carried from attempt 2") {
				t.Errorf("notes = %q", b.Notes)
			}
			if a := reviewerOf(t, dec, "rev-a"); a.Accept || a.CarriedFrom != 0 {
				t.Errorf("rev-a verdict = %+v, want a fresh reject", a)
			}
			for i := 1; i < len(dec.Reviewers); i++ {
				if dec.Reviewers[i-1].Order > dec.Reviewers[i].Order {
					t.Errorf("verdicts out of order: %+v", dec.Reviewers)
				}
			}
		})
	}
}

// AC4: each way a prior accept cannot be trusted forces a fresh dispatch.
func TestCarry_UntrustedPriorIsDispatched(t *testing.T) {
	cases := []struct {
		name string
		b    PriorVerdict
	}{
		{"latest is a reject", rejected("rev-b")},
		{"empty reviewed", accepted("rev-b", "")},
		{"truncated", PriorVerdict{Skill: "rev-b", Decision: "accept", Reviewed: "PLAN-B", ReviewedTruncated: true}},
		{"stale", PriorVerdict{Skill: "rev-b", Decision: "accept", Reviewed: "PLAN-B", ReviewedStale: true}},
		{"quotation no longer in the material", accepted("rev-b", "WORDS-THE-PLAN-NO-LONGER-HAS")},
	}
	for _, tc := range cases {
		for _, sc := range carrySchedulers {
			t.Run(tc.name+"/"+sc.name, func(t *testing.T) {
				// rev-a is carried; rev-c rejects and keeps the edge from advancing.
				prior := []PriorVerdict{accepted("rev-a", "PLAN-A"), tc.b, rejected("rev-c")}
				g, r := carryEngine(t, sc.par, sc.extra, prior, map[string]string{"rev-c": "reject"})
				dec := carryGate(t, g)
				if r.sessionsFor("rev-b") != 1 {
					t.Errorf("rev-b must be dispatched once, got %d", r.sessionsFor("rev-b"))
				}
				if b := reviewerOf(t, dec, "rev-b"); b.CarriedFrom != 0 {
					t.Errorf("rev-b verdict must be fresh: %+v", b)
				}
				if r.sessionsFor("rev-a") != 0 {
					t.Errorf("rev-a (a trusted accept) was dispatched %d time(s)", r.sessionsFor("rev-a"))
				}
			})
		}
	}
}

// AC4: a functional check is a command, never a carried verdict.
func TestCarry_FunctionalCheckIsAlwaysRun(t *testing.T) {
	wf := spineWF("", "", "", "in_progress|executor||rev-a,chk,rev-c|reviewer|0|"+carryKey, "done")
	skills := append(bundleSkillDocs("rev-a", "rev-c"), docindex.Doc{Kind: "skills", Name: "chk", Body: bundleCheckSkill})
	r := &bundleRunner{verdict: map[string]string{"rev-c": "reject"}}
	g := New(r, fakeDocs{workflow: wf, extraSkills: skills}, "/repo", "")
	ran := 0
	g.check = func(context.Context, string, string, string) (string, error) { ran++; return "ok\n", nil }
	g.SetDocsResolver(func(context.Context, string) []DocState {
		return []DocState{{Name: "plan", Type: "plan", Body: carryPlan + " PLAN-CHK"}}
	})
	g.SetPriorVerdictsResolver(func(context.Context, string, string, string, verb.StaleExemption) []PriorVerdict {
		return []PriorVerdict{accepted("rev-a", "PLAN-A"), accepted("chk", "PLAN-CHK"), rejected("rev-c")}
	})
	dec := carryGate(t, g)
	if ran != 1 {
		t.Errorf("the check must run, ran %d", ran)
	}
	if rv := reviewerOf(t, dec, "chk"); rv.CarriedFrom != 0 {
		t.Errorf("a functional check is never carried: %+v", rv)
	}
	if rv := reviewerOf(t, dec, "rev-a"); rv.CarriedFrom == 0 {
		t.Errorf("rev-a is a trusted accept and should be carried: %+v", rv)
	}
}

// AC4: a panel (or fold) is never carried.
func TestCarry_PanelIsNeverCarried(t *testing.T) {
	g, _ := carryEngine(t, "0", "", []PriorVerdict{accepted("rev-a", "PLAN-A"), accepted("rev-b", "PLAN-B")}, nil)
	ordered := []reviewerRef{
		{skill: "rev-a", panel: []string{"s1", "s2"}, combine: "fold"},
		{skill: "rev-b"},
	}
	item := workitem.Item{ID: "sty_c", Status: "backlog", Category: "feature"}
	carried, fresh := g.carryable(context.Background(), item, "in_progress", ordered, 2)
	if len(carried) != 1 || carried[0].verdict.Skill != "rev-b" || len(fresh) != 1 || fresh[0] != 0 {
		t.Errorf("carried = %+v fresh = %v, want only rev-b carried", carried, fresh)
	}
}

// AC5 + AC7: once every dispatched reviewer accepts, the carried reviewers are
// dispatched fresh in the same call, so the accepting round holds a fresh
// verdict from every reviewer.
func TestCarry_ConfirmingPassDispatchesCarriedReviewers(t *testing.T) {
	prior := []PriorVerdict{rejected("rev-a"), accepted("rev-b", "PLAN-B"), accepted("rev-c", "PLAN-C")}
	for _, sc := range carrySchedulers {
		t.Run(sc.name, func(t *testing.T) {
			g, r := carryEngine(t, sc.par, sc.extra, prior, nil)
			dec := carryGate(t, g)
			if !dec.Accept || !dec.Gated {
				t.Fatalf("every reviewer accepts, the edge must advance: %+v", dec)
			}
			for _, sk := range []string{"rev-a", "rev-b", "rev-c"} {
				if n := r.sessionsFor(sk); n != 1 {
					t.Errorf("%s dispatched %d time(s), want 1", sk, n)
				}
			}
			if len(dec.Reviewers) != 3 {
				t.Fatalf("reviewers = %+v", dec.Reviewers)
			}
			for i, rv := range dec.Reviewers {
				if rv.CarriedFrom != 0 || !rv.Accept || rv.Order != i {
					t.Errorf("verdict %d = %+v, want a fresh accept in order", i, rv)
				}
			}
		})
	}
}

// AC5: a reject in the confirming pass blocks the edge as an ordinary reject.
func TestCarry_ConfirmingPassRejectBlocksTheEdge(t *testing.T) {
	prior := []PriorVerdict{rejected("rev-a"), accepted("rev-b", "PLAN-B")}
	g, r := carryEngine(t, "0", "", prior, map[string]string{"rev-b": "reject"})
	dec := carryGate(t, g)
	if dec.Accept {
		t.Fatalf("the confirming pass rejected: %+v", dec)
	}
	if r.sessionsFor("rev-a") != 1 || r.sessionsFor("rev-b") != 1 {
		t.Errorf("a=%d b=%d, want each dispatched once", r.sessionsFor("rev-a"), r.sessionsFor("rev-b"))
	}
	if dec.Skill != "rev-b" {
		t.Errorf("top-level skill = %q, want the rejecting rev-b", dec.Skill)
	}
}

// AC5: when every reviewer is carryable carry is moot — all are dispatched.
func TestCarry_AllCarryableDispatchesEverything(t *testing.T) {
	prior := []PriorVerdict{accepted("rev-a", "PLAN-A"), accepted("rev-b", "PLAN-B"), accepted("rev-c", "PLAN-C")}
	g, r := carryEngine(t, "0", "", prior, nil)
	dec := carryGate(t, g)
	for _, sk := range []string{"rev-a", "rev-b", "rev-c"} {
		if r.sessionsFor(sk) != 1 {
			t.Errorf("%s dispatched %d time(s), want 1", sk, r.sessionsFor(sk))
		}
	}
	for _, rv := range dec.Reviewers {
		if rv.CarriedFrom != 0 {
			t.Errorf("nothing is carried when everything could be: %+v", rv)
		}
	}
}

// AC6: a step with no reviewer_carry dispatches every reviewer every round.
func TestCarry_AbsentKeyDispatchesEveryReviewer(t *testing.T) {
	wf := spineWF("", "", "", "in_progress|executor||rev-a,rev-b,rev-c|reviewer|0", "done")
	r := &bundleRunner{verdict: map[string]string{"rev-a": "reject"}}
	g := New(r, fakeDocs{workflow: wf, extraSkills: bundleSkillDocs("rev-a", "rev-b", "rev-c")}, "/repo", "")
	g.SetDocsResolver(func(context.Context, string) []DocState {
		return []DocState{{Name: "plan", Type: "plan", Body: carryPlan}}
	})
	g.SetPriorVerdictsResolver(func(context.Context, string, string, string, verb.StaleExemption) []PriorVerdict {
		return []PriorVerdict{rejected("rev-a"), accepted("rev-b", "PLAN-B")}
	})
	g.backoff = func(int) time.Duration { return 0 }
	dec := carryGate(t, g)
	if r.sessionsFor("rev-a") != 1 {
		t.Errorf("rev-a dispatched %d time(s), want 1", r.sessionsFor("rev-a"))
	}
	for _, rv := range dec.Reviewers {
		if rv.CarriedFrom != 0 {
			t.Errorf("no key, no carry: %+v", rv)
		}
	}
}

// AC2: staleExemption is zero unless the step declares reviewer_carry and
// dispatches a named performer; then it names that performer's output document
// and agent.
func TestStaleExemption(t *testing.T) {
	const contract = "---\nname: architecture-alignment\ntype: skill\ndescription: x\noutput_name: design\noutput_type: design-note\n---\nbody"
	bindings := map[string]config.AgentBinding{"architect": {Command: "fake -p {system}", Tools: "Read"}}
	cases := []struct {
		name  string
		step  string
		want  verb.StaleExemption
		extra string
	}{
		{"key absent", "named|architect|architecture-alignment|||", verb.StaleExemption{}, ""},
		{"key declared", "named|architect|architecture-alignment||||" + carryKey, verb.StaleExemption{DocName: "design", Performer: "architect"}, ""},
		{"key declared, no dispatched performer", "named|executor||||||" + carryKey, verb.StaleExemption{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := spineWF("", "", "", tc.step, "done")
			g, _ := newEngine(t, "ok", fakeDocs{workflow: wf, skillBody: contract, skillFound: true})
			g.SetNamedAgents(func(name string) (config.AgentBinding, bool) {
				b, ok := bindings[name]
				return b, ok
			})
			got := g.staleExemption(context.Background(), workitem.Item{ID: "sty_1", Status: "backlog"}, "named")
			if got != tc.want {
				t.Errorf("staleExemption = %+v, want %+v", got, tc.want)
			}
		})
	}
}
