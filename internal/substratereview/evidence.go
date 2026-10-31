package substratereview

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// Evidence is what a repo's story history says about one substrate item. Every
// figure is an enumeration of ledger rows the binary already wrote; whether a
// figure argues for a cut is the judge's call, never decided here.
type Evidence struct {
	// Presentations is how many presentations of an edge carried a row this item
	// judged (a verdict or a reviewer invocation).
	Presentations int `json:"presentations"`
	// Rejections is how many of those presentations this item rejected, and
	// Reasons the notes each rejection gave, oldest first.
	Rejections int      `json:"rejections"`
	Reasons    []string `json:"reasons,omitempty"`
	// Amended, Reversed and Represented split the rejections by what followed:
	// a definition edit before the edge was next accepted (amended), the very
	// next presentation accepted with no edit (reversed), or a later
	// presentation accepted with no edit (re-presented to accept). Each share is
	// a percentage of Rejections, 0 when there were none.
	Amended          int     `json:"amended"`
	AmendedShare     float64 `json:"amended_share"`
	Reversed         int     `json:"reversed"`
	ReversedShare    float64 `json:"reversed_share"`
	Represented      int     `json:"represented_to_accept"`
	RepresentedShare float64 `json:"represented_share"`
	// Parks counts transitions to a park state whose preceding presentation this
	// item rejected: a reject budget spent on its objection.
	Parks int `json:"parks"`
	// Rework counts performer re-runs after a rejected exit (ledger.Redispatches)
	// that this item's rejection caused, or that this agent performed.
	Rework int `json:"rework"`
	// Flaky counts presentations whose seats returned no verdict at all
	// (ledger.Presentation.NoVerdict) and that ran this item. Never rework.
	Flaky int `json:"flaky"`
	// Dissent lists each panel seat whose verdict differed from its round's
	// outcome. DissentRecorded is false when no panel round this item judged
	// carried per-seat verdicts: the ledger then has nothing to say about
	// dissent, which is not the same as "no dissent".
	Dissent         []Dissent `json:"dissent,omitempty"`
	DissentRecorded bool      `json:"dissent_recorded"`
	// Stories are the story ids whose history shows any of the above.
	Stories []string `json:"stories,omitempty"`
	// Models breaks the verdicts and invocations down by the model that ran each
	// seat, read from the agent_invocation and verdict rows.
	Models []ModelRow `json:"models,omitempty"`
	// CurrentModels is each seat that runs this item now, with the model its
	// binding currently names ("" when the binding names none).
	CurrentModels map[string]string `json:"current_models,omitempty"`
}

// Dissent is one seat that disagreed with its panel round's outcome.
type Dissent struct {
	Story    string `json:"story"`
	Seat     string `json:"seat"`
	Model    string `json:"model"`
	Accepted bool   `json:"accepted"`
	Notes    string `json:"notes,omitempty"`
}

// ModelRow is one model's share of an item's history.
type ModelRow struct {
	Model       string   `json:"model"`
	Invocations int      `json:"invocations"`
	Accepts     int      `json:"accepts"`
	Rejections  int      `json:"rejections"`
	Stories     []string `json:"stories,omitempty"`
}

// Selector names the ledger rows that are evidence for one item. Empty fields
// are wildcards; a selector with every field empty selects nothing (a principle
// is judged by no gate row of its own).
type Selector struct {
	// State selects the edges into this status.
	State string `json:"state,omitempty"`
	// Skill selects the rows a gate or performer skill of this name wrote.
	Skill string `json:"skill,omitempty"`
	// Agent selects the rows of this agents.toml seat (a panel seat, or the
	// agent role an invocation row names).
	Agent string `json:"agent,omitempty"`
}

func (s Selector) empty() bool { return s.State == "" && s.Skill == "" && s.Agent == "" }

// EvidenceOptions carries what the history alone cannot say.
type EvidenceOptions struct {
	// ParkStates are the statuses a park transition enters (the route's park
	// exits). Empty means "blocked".
	ParkStates []string
	// Bound maps each agents.toml seat to the model its binding names now. It
	// attributes a panel seat's verdict to a model when the seat's own row does
	// not record one, and fills CurrentModels.
	Bound map[string]string
}

// Unrecorded is the model a seat verdict is attributed to when no row says
// which model ran it.
const Unrecorded = "unrecorded"

// rowPayload is the subset of a verdict, invocation or transition row's payload
// this package reads. Extra keys are ignored.
type rowPayload struct {
	From          string        `json:"from"`
	To            string        `json:"to"`
	Skill         string        `json:"skill"`
	Notes         string        `json:"notes"`
	Agent         string        `json:"agent"`
	Model         string        `json:"model"`
	ModelResolved string        `json:"model_resolved"`
	Role          string        `json:"role"`
	Seats         []seatPayload `json:"seats"`
}

type seatPayload struct {
	Seat          string `json:"seat"`
	Accept        bool   `json:"accept"`
	Notes         string `json:"notes"`
	Model         string `json:"model"`
	ModelResolved string `json:"model_resolved"`
}

func decode(e ledger.Entry) rowPayload {
	var p rowPayload
	if len(e.Payload) > 0 {
		_ = json.Unmarshal(e.Payload, &p)
	}
	return p
}

func modelOf(model, resolved string) string {
	if strings.TrimSpace(resolved) != "" && resolved != "unavailable" {
		return resolved
	}
	if strings.TrimSpace(model) != "" {
		return model
	}
	return ""
}

// seatVerdict is one seat's verdict inside a verdict row.
type seatVerdict struct {
	seat, model, notes string
	rejected           bool
}

type rowFacts struct {
	skill, agent, notes string
	reject, panel       bool
	seats               []seatVerdict
}

type invFact struct{ skill, agent, model string }

type presFacts struct {
	from, to                    string
	first, last                 int
	rejected, noVerdict         bool
	amended, reversed, repToAcc bool
	rows                        []rowFacts
	invs                        []invFact
}

type reworkFact struct{ agent, from, rejSkill, rejTo string }

type storyFacts struct {
	id      string
	pres    []presFacts
	reworks []reworkFact
	parks   []int // indexes into pres of the presentation each park followed
}

// facts folds one story's rows once, so every item is matched against the
// folded story rather than re-decoding the ledger per item.
func facts(id string, entries []ledger.Entry, opt EvidenceOptions) storyFacts {
	sf := storyFacts{id: id}
	var edits []int
	for i, e := range entries {
		if e.Kind == ledger.KindDefinitionEdited || e.Kind == ledger.KindDefinitionAmended {
			edits = append(edits, i)
		}
	}
	editBetween := func(after, before int) bool {
		for _, i := range edits {
			if i > after && i < before {
				return true
			}
		}
		return false
	}
	for _, edge := range ledger.GatedEdges(entries) {
		ps := ledger.EdgePresentations(entries, edge[0], edge[1])
		base := len(sf.pres)
		for _, p := range ps {
			pf := presFacts{from: edge[0], to: edge[1], first: len(entries), last: -1, rejected: p.Rejected, noVerdict: p.NoVerdict}
			invModels := map[string][]string{}
			for _, i := range p.Invocations {
				r := decode(entries[i])
				m := modelOf(r.Model, r.ModelResolved)
				for _, sk := range strings.Split(r.Skill, "+") {
					pf.invs = append(pf.invs, invFact{skill: sk, agent: r.Agent, model: m})
					invModels[sk] = appendUnique(invModels[sk], m)
				}
				pf.first = min(pf.first, i)
				pf.last = max(pf.last, i)
			}
			for _, i := range p.Verdicts {
				pf.first = min(pf.first, i)
				pf.last = max(pf.last, i)
				e := entries[i]
				if e.Kind == ledger.KindReadinessAccept {
					continue
				}
				r := decode(e)
				rf := rowFacts{skill: r.Skill, agent: r.Agent, notes: r.Notes,
					reject: e.Kind == ledger.KindReviewReject || e.Kind == ledger.KindPerformerReject}
				if len(r.Seats) > 0 {
					rf.panel = true
					for _, s := range r.Seats {
						m := modelOf(s.Model, s.ModelResolved)
						if m == "" {
							m = seatModel(s.Seat, invModels[r.Skill], opt.Bound)
						}
						rf.seats = append(rf.seats, seatVerdict{seat: s.Seat, model: m, notes: s.Notes, rejected: !s.Accept})
					}
				} else {
					agent := r.Agent
					if agent == "" {
						agent = "reviewer"
					}
					m := modelOf(r.Model, r.ModelResolved)
					if m == "" {
						m = Unrecorded
					}
					rf.seats = []seatVerdict{{seat: agent, model: m, notes: r.Notes, rejected: rf.reject}}
				}
				pf.rows = append(pf.rows, rf)
			}
			sf.pres = append(sf.pres, pf)
		}
		// What followed each rejection on this edge.
		for k := base; k < len(sf.pres); k++ {
			if !sf.pres[k].rejected {
				continue
			}
			next := -1
			for m := k + 1; m < len(sf.pres); m++ {
				if !sf.pres[m].rejected && !sf.pres[m].noVerdict {
					next = m
					break
				}
			}
			end := len(entries)
			if next >= 0 {
				end = sf.pres[next].first
			}
			switch {
			case editBetween(sf.pres[k].last, end):
				sf.pres[k].amended = true
			case next == k+1:
				sf.pres[k].reversed = true
			case next > k+1:
				sf.pres[k].repToAcc = true
			}
		}
	}
	// Rework: each performer re-run after a rejected exit, attributed to the
	// rejection that caused it.
	for _, i := range ledger.Redispatches(entries) {
		r := decode(entries[i])
		rw := reworkFact{agent: r.Agent, from: r.From}
		for j := i - 1; j >= 0; j-- {
			if ledger.IsExitReject(entries[j]) {
				if rj := decode(entries[j]); rj.From == r.From {
					rw.rejSkill, rw.rejTo = rj.Skill, rj.To
					break
				}
			}
		}
		sf.reworks = append(sf.reworks, rw)
	}
	// Parks: a transition into a park state after a rejected presentation of an
	// edge leaving the status it parked from.
	parks := opt.ParkStates
	if len(parks) == 0 {
		parks = []string{"blocked"}
	}
	for i, e := range entries {
		if e.Kind != ledger.KindStatusTransition {
			continue
		}
		r := decode(e)
		if !contains(parks, r.To) {
			continue
		}
		at := -1
		for k, p := range sf.pres {
			if p.from == r.From && p.last < i && (at < 0 || p.last > sf.pres[at].last) {
				at = k
			}
		}
		if at >= 0 && sf.pres[at].rejected {
			sf.parks = append(sf.parks, at)
		}
	}
	return sf
}

// seatModel attributes a panel seat to a model when its own row records none:
// the one model every invocation of the round ran, else the seat's bound model
// when an invocation of the round ran it, else Unrecorded.
func seatModel(seat string, invModels []string, bound map[string]string) string {
	var known []string
	for _, m := range invModels {
		if m != "" {
			known = append(known, m)
		}
	}
	if len(known) == 1 {
		return known[0]
	}
	if b := bound[seat]; b != "" {
		for _, m := range known {
			if m == b || strings.Contains(m, b) {
				return m
			}
		}
	}
	return Unrecorded
}

func (s Selector) matchRow(p presFacts, r rowFacts) bool {
	if s.empty() || (s.State != "" && p.to != s.State) || (s.Skill != "" && r.skill != s.Skill) {
		return false
	}
	if s.Agent == "" {
		return true
	}
	if r.agent == s.Agent {
		return true
	}
	for _, sv := range r.seats {
		if r.panel && sv.seat == s.Agent {
			return true
		}
	}
	return false
}

func (s Selector) matchInv(p presFacts, v invFact) bool {
	return !s.empty() && (s.State == "" || p.to == s.State) && (s.Skill == "" || v.skill == s.Skill) && (s.Agent == "" || v.agent == s.Agent)
}

// seats are the seat verdicts of r the selector speaks for: one seat when it
// names a panel seat, every seat otherwise.
func (s Selector) seats(r rowFacts) []seatVerdict {
	if s.Agent == "" || !r.panel {
		return r.seats
	}
	var out []seatVerdict
	for _, sv := range r.seats {
		if sv.seat == s.Agent {
			out = append(out, sv)
		}
	}
	return out
}

// rejection reports whether the selector's view of r rejected, with the
// reasons: the seat's own notes for a panel seat, the rejecting seats' notes
// for a panel gate, the row's notes otherwise.
func (s Selector) rejection(r rowFacts) (bool, []string) {
	if r.panel {
		if s.Agent != "" {
			var reasons []string
			rejected := false
			for _, sv := range s.seats(r) {
				if sv.rejected {
					rejected = true
					reasons = append(reasons, oneLine(sv.notes))
				}
			}
			return rejected, reasons
		}
		if !r.reject {
			return false, nil
		}
		var reasons []string
		for _, sv := range r.seats {
			if sv.rejected {
				reasons = append(reasons, sv.seat+": "+oneLine(sv.notes))
			}
		}
		if len(reasons) == 0 {
			reasons = []string{oneLine(r.notes)}
		}
		return true, reasons
	}
	if !r.reject {
		return false, nil
	}
	return true, []string{oneLine(r.notes)}
}

func (s Selector) matchRework(rw reworkFact) bool {
	if s.empty() {
		return false
	}
	if s.Agent != "" {
		return rw.agent == s.Agent
	}
	return rw.rejTo != "" && (s.State == "" || rw.rejTo == s.State) && (s.Skill == "" || rw.rejSkill == s.Skill)
}

// History is a repo's folded story history, ready to be matched per item.
type History struct {
	stories []storyFacts
	opt     EvidenceOptions
	// Orchestrator is the model the latest in-loop session_model row names.
	Orchestrator string
}

// Fold reads every story's rows once. entries maps a story id to its ledger
// rows, oldest first.
func Fold(entries map[string][]ledger.Entry, opt EvidenceOptions) History {
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	h := History{opt: opt}
	var latest ledger.Entry
	for _, id := range ids {
		h.stories = append(h.stories, facts(id, entries[id], opt))
		for _, e := range entries[id] {
			if e.Kind != ledger.KindSessionModel {
				continue
			}
			if r := decode(e); r.Role == "in-loop" && r.Model != "" && r.Model != "unknown" && !e.CreatedAt.Before(latest.CreatedAt) {
				latest = e
			}
		}
	}
	if latest.Kind != "" {
		h.Orchestrator = decode(latest).Model
	}
	return h
}

// For returns the evidence for one item: the rows its selector names, and the
// current model of each seat that runs it.
func (h History) For(sel Selector, seats []string) Evidence {
	var ev Evidence
	stories := map[string]bool{}
	models := map[string]*ModelRow{}
	model := func(name, story string) *ModelRow {
		m, ok := models[name]
		if !ok {
			m = &ModelRow{Model: name}
			models[name] = m
		}
		m.Stories = appendUnique(m.Stories, story)
		return m
	}
	for _, sf := range h.stories {
		for k, p := range sf.pres {
			matched, rejected := false, false
			var reasons []string
			for _, r := range p.rows {
				if !sel.matchRow(p, r) {
					continue
				}
				matched = true
				if rej, why := sel.rejection(r); rej {
					rejected = true
					reasons = append(reasons, why...)
				}
				for _, sv := range sel.seats(r) {
					m := model(sv.model, sf.id)
					if sv.rejected {
						m.Rejections++
					} else {
						m.Accepts++
					}
				}
				if r.panel {
					ev.DissentRecorded = true
					for _, sv := range sel.seats(r) {
						if sv.rejected != p.rejected {
							ev.Dissent = append(ev.Dissent, Dissent{Story: sf.id, Seat: sv.seat, Model: sv.model, Accepted: !sv.rejected, Notes: oneLine(sv.notes)})
						}
					}
				}
			}
			ran := false
			for _, v := range p.invs {
				if sel.matchInv(p, v) {
					ran = true
					m := v.model
					if m == "" {
						m = Unrecorded
					}
					model(m, sf.id).Invocations++
				}
			}
			if !matched && !ran {
				continue
			}
			ev.Presentations++
			stories[sf.id] = true
			if p.noVerdict && ran {
				ev.Flaky++
			}
			if rejected {
				ev.Rejections++
				ev.Reasons = append(ev.Reasons, reasons...)
				switch {
				case p.amended:
					ev.Amended++
				case p.reversed:
					ev.Reversed++
				case p.repToAcc:
					ev.Represented++
				}
				for _, at := range sf.parks {
					if at == k {
						ev.Parks++
					}
				}
			}
		}
		for _, rw := range sf.reworks {
			if sel.matchRework(rw) {
				ev.Rework++
				stories[sf.id] = true
			}
		}
	}
	if ev.Rejections > 0 {
		pct := func(n int) float64 { return float64(n) * 100 / float64(ev.Rejections) }
		ev.AmendedShare, ev.ReversedShare, ev.RepresentedShare = pct(ev.Amended), pct(ev.Reversed), pct(ev.Represented)
	}
	for id := range stories {
		ev.Stories = append(ev.Stories, id)
	}
	sort.Strings(ev.Stories)
	for _, m := range models {
		ev.Models = append(ev.Models, *m)
	}
	sort.Slice(ev.Models, func(i, j int) bool { return ev.Models[i].Model < ev.Models[j].Model })
	for _, s := range seats {
		if ev.CurrentModels == nil {
			ev.CurrentModels = map[string]string{}
		}
		ev.CurrentModels[s] = h.opt.Bound[s]
	}
	return ev
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func appendUnique(xs []string, x string) []string {
	if contains(xs, x) {
		return xs
	}
	return append(xs, x)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
