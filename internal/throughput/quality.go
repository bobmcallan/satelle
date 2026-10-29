package throughput

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Ratio is a figure that is not a whole count — a share or a mean. Value is nil
// when it could not be computed, and Reason then says why.
type Ratio struct {
	Value  *float64 `json:"value"`
	Reason string   `json:"reason,omitempty"`
}

func ratio(v float64) Ratio { return Ratio{Value: &v} }

// Pass is how often one step (or one gate) accepted on its first presentation,
// over the finished stories that presented it.
type Pass struct {
	Name            string   `json:"name"`
	Presented       int      `json:"presented"`
	Accepted        int      `json:"accepted"`
	Share           Ratio    `json:"share"`
	AcceptedStories []string `json:"accepted_stories"`
	RejectedStories []string `json:"rejected_stories"`
}

// PassTable is the first-pass rows, in route order for steps and by name for
// gates. Reason is set when there are none to give.
type PassTable struct {
	Rows   []Pass `json:"rows"`
	Reason string `json:"reason,omitempty"`
}

// EdgeRounds is the rejected presentations per story on one gated edge, over the
// finished stories that presented it.
type EdgeRounds struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Stories int    `json:"stories"`
	Mean    Ratio  `json:"mean"`
	Max     Count  `json:"max"`
	// AtMax are the stories with Max rejected rounds; Rejected every story with
	// at least one.
	AtMax    []string `json:"at_max"`
	Rejected []string `json:"rejected"`
}

// RoundsTable is the per-edge rounds, in route order. Reason is set when there
// are none to give.
type RoundsTable struct {
	Rows   []EdgeRounds `json:"rows"`
	Reason string       `json:"reason,omitempty"`
}

// Redispatch is the performer re-runs of a step after an exit of it was
// rejected (ledger.Redispatches).
type Redispatch struct {
	Count   Count    `json:"count"`
	Stories []string `json:"stories"`
}

// SkillRejects is one gate's rejects and how many of them named no criterion.
type SkillRejects struct {
	Skill       string   `json:"skill"`
	Rejects     int      `json:"rejects"`
	NoCriterion int      `json:"no_criterion"`
	Stories     []string `json:"stories"`
}

// NoCriterion is the reviewer rejects that name no criterion: no criteria entry
// numbered 1 or higher with status reject or unmet. Count is out of Rejects.
type NoCriterion struct {
	Rejects int            `json:"rejects"`
	Count   Count          `json:"count"`
	Stories []string       `json:"stories"`
	BySkill []SkillRejects `json:"by_skill"`
}

// CameBackPair is a story created after the story it cites reached a terminal
// state.
type CameBackPair struct {
	Story string `json:"story"`
	Cites string `json:"cites"`
}

// CameBack is the work that came back: every CameBackPair among the stories
// created in the window.
type CameBack struct {
	Count   Count          `json:"count"`
	Pairs   []CameBackPair `json:"pairs"`
	Stories []string       `json:"stories"`
}

// QualityReport is the quality figures of one repo, or of the total.
type QualityReport struct {
	FirstPassByStep PassTable   `json:"first_pass_by_step"`
	FirstPassByGate PassTable   `json:"first_pass_by_gate"`
	Rounds          RoundsTable `json:"rounds"`
	Redispatches    Redispatch  `json:"redispatches"`
	NoCriterion     NoCriterion `json:"no_criterion_rejects"`
	CameBack        CameBack    `json:"came_back"`
}

// Returns is what work that came back is counted from: the stories created in
// the window, and when each story they cite last reached a terminal state.
type Returns struct {
	Created []Created
	Done    map[string]time.Time
}

// Created is one story created in the window and the story ids it cites.
type Created struct {
	ID    string
	At    time.Time
	Cites []string
}

var storyIDPattern = regexp.MustCompile(`\bsty_[0-9a-f]{8}\b`)

// Cites is the story ids it names in its title, body, acceptance criteria and
// tags, sorted, leaving out its own id and its parent's.
func Cites(it workitem.Item) []string {
	text := strings.Join(append([]string{it.Title, it.Body, it.AcceptanceCriteria}, it.Tags...), "\n")
	set := map[string]bool{}
	for _, id := range storyIDPattern.FindAllString(text, -1) {
		if id != it.ID && id != it.ParentID {
			set[id] = true
		}
	}
	return sorted(set)
}

// cited is every id the created stories cite that known has no done time for,
// sorted.
func (r Returns) cited(known map[string]time.Time) []string {
	set := map[string]bool{}
	for _, c := range r.Created {
		for _, id := range c.Cites {
			if _, ok := known[id]; !ok {
				set[id] = true
			}
		}
	}
	return sorted(set)
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Quality computes the quality figures over the finished items and the stories
// created in the window.
func Quality(items []Item, ret Returns) QualityReport {
	if len(items) == 0 {
		q := unavailableQuality(ReasonNoFinished)
		q.CameBack = cameBack(ret)
		return q
	}
	q := QualityReport{CameBack: cameBack(ret)}
	q.FirstPassByStep, q.FirstPassByGate = firstPass(items)
	q.Rounds = rounds(items)
	q.Redispatches = redispatches(items)
	q.NoCriterion = noCriterion(items)
	return q
}

// unavailableQuality is a QualityReport whose every figure is unavailable for
// reason.
func unavailableQuality(reason string) QualityReport {
	none := Count{Reason: reason}
	return QualityReport{
		FirstPassByStep: PassTable{Rows: []Pass{}, Reason: reason},
		FirstPassByGate: PassTable{Rows: []Pass{}, Reason: reason},
		Rounds:          RoundsTable{Rows: []EdgeRounds{}, Reason: reason},
		Redispatches:    Redispatch{Count: none, Stories: []string{}},
		NoCriterion:     NoCriterion{Count: none, Stories: []string{}, BySkill: []SkillRejects{}},
		CameBack:        CameBack{Count: none, Pairs: []CameBackPair{}, Stories: []string{}},
	}
}

// spineRank is name's position on spec's route; a name off the route sorts last.
func spineRank(spec wfdot.Spec, name string) int {
	if i := spec.SpineIndex(name); i >= 0 {
		return i
	}
	return math.MaxInt
}

type passTally struct {
	rank                int
	presented, accepted int
	acc, rej            map[string]bool
}

func passOf(by map[string]*passTally, key string, rank int) *passTally {
	t, ok := by[key]
	if !ok {
		t = &passTally{rank: rank, acc: map[string]bool{}, rej: map[string]bool{}}
		by[key] = t
	}
	t.rank = min(t.rank, rank)
	return t
}

func (t *passTally) add(id string, accepted bool) {
	t.presented++
	if accepted {
		t.accepted++
		t.acc[id] = true
	} else {
		t.rej[id] = true
	}
}

// firstPass tallies, for every gated edge a finished item presented, its first
// presentation with a verdict: per step (the edge's destination) whether the
// presentation was accepted, and per gate whether every verdict row of that
// gate in it accepted.
func firstPass(items []Item) (PassTable, PassTable) {
	steps, gates := map[string]*passTally{}, map[string]*passTally{}
	for _, it := range items {
		for _, edge := range ledger.GatedEdges(it.Rows) {
			for _, p := range ledger.EdgePresentations(it.Rows, edge[0], edge[1]) {
				if p.NoVerdict {
					continue
				}
				passOf(steps, edge[1], spineRank(it.Spec, edge[1])).add(it.ID, p.Accepted)
				for skill, accepted := range gateVerdicts(it.Rows, p) {
					passOf(gates, skill, 0).add(it.ID, accepted)
				}
				break
			}
		}
	}
	return passTable(steps), passTable(gates)
}

// gateVerdicts is, per gate skill with a reviewer verdict in p, whether every
// one of its verdict rows accepted.
func gateVerdicts(rows []ledger.Entry, p ledger.Presentation) map[string]bool {
	out := map[string]bool{}
	for _, i := range p.Verdicts {
		e := rows[i]
		if e.Kind != ledger.KindReviewAccept && e.Kind != ledger.KindReviewReject {
			continue
		}
		skill := rowSkill(e)
		if skill == "" {
			continue
		}
		prev, seen := out[skill]
		out[skill] = (!seen || prev) && e.Kind == ledger.KindReviewAccept
	}
	return out
}

func rowSkill(e ledger.Entry) string {
	var p struct {
		Skill string `json:"skill"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	return p.Skill
}

func passTable(by map[string]*passTally) PassTable {
	if len(by) == 0 {
		return PassTable{Rows: []Pass{}, Reason: ReasonNoPresentation}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := by[keys[i]], by[keys[j]]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		return keys[i] < keys[j]
	})
	out := PassTable{Rows: []Pass{}}
	for _, k := range keys {
		t := by[k]
		out.Rows = append(out.Rows, Pass{
			Name: k, Presented: t.presented, Accepted: t.accepted,
			Share:           ratio(float64(t.accepted) / float64(t.presented)),
			AcceptedStories: sorted(t.acc), RejectedStories: sorted(t.rej),
		})
	}
	return out
}

type roundTally struct {
	rank   [2]int
	counts map[string]int
}

// rounds counts, per gated edge, each finished item's rejected presentations of
// it over the item's whole history. An item that presented the edge with no
// rejection counts 0.
func rounds(items []Item) RoundsTable {
	by := map[[2]string]*roundTally{}
	for _, it := range items {
		for _, edge := range ledger.GatedEdges(it.Rows) {
			n := 0
			for _, p := range ledger.EdgePresentations(it.Rows, edge[0], edge[1]) {
				if p.Rejected {
					n++
				}
			}
			rank := [2]int{spineRank(it.Spec, edge[0]), spineRank(it.Spec, edge[1])}
			t, ok := by[edge]
			if !ok {
				t = &roundTally{rank: rank, counts: map[string]int{}}
				by[edge] = t
			}
			if rank[0] < t.rank[0] || rank[0] == t.rank[0] && rank[1] < t.rank[1] {
				t.rank = rank
			}
			t.counts[it.ID] += n
		}
	}
	if len(by) == 0 {
		return RoundsTable{Rows: []EdgeRounds{}, Reason: ReasonNoPresentation}
	}
	edges := make([][2]string, 0, len(by))
	for e := range by {
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		a, b := by[edges[i]].rank, by[edges[j]].rank
		if a != b {
			return a[0] < b[0] || a[0] == b[0] && a[1] < b[1]
		}
		return edges[i][0]+"\x00"+edges[i][1] < edges[j][0]+"\x00"+edges[j][1]
	})
	out := RoundsTable{Rows: []EdgeRounds{}}
	for _, e := range edges {
		counts := by[e].counts
		sum, most := 0, 0
		for _, n := range counts {
			sum += n
			most = max(most, n)
		}
		atMax, rejected := map[string]bool{}, map[string]bool{}
		for id, n := range counts {
			if n == most {
				atMax[id] = true
			}
			if n > 0 {
				rejected[id] = true
			}
		}
		out.Rows = append(out.Rows, EdgeRounds{
			From: e[0], To: e[1], Stories: len(counts),
			Mean: ratio(float64(sum) / float64(len(counts))), Max: counted(most),
			AtMax: sorted(atMax), Rejected: sorted(rejected),
		})
	}
	return out
}

func redispatches(items []Item) Redispatch {
	exitRejected, n := false, 0
	set := map[string]bool{}
	for _, it := range items {
		for _, e := range it.Rows {
			if ledger.IsExitReject(e) {
				exitRejected = true
				break
			}
		}
		if r := ledger.Redispatches(it.Rows); len(r) > 0 {
			n += len(r)
			set[it.ID] = true
		}
	}
	if !exitRejected {
		return Redispatch{Count: Count{Reason: ReasonNoExitReject}, Stories: []string{}}
	}
	return Redispatch{Count: counted(n), Stories: sorted(set)}
}

type skillTally struct {
	rejects, none int
	stories       map[string]bool
}

func noCriterion(items []Item) NoCriterion {
	rejects, none := 0, 0
	set := map[string]bool{}
	by := map[string]*skillTally{}
	for _, it := range items {
		for _, e := range it.Rows {
			if e.Kind != ledger.KindReviewReject {
				continue
			}
			rejects++
			skill := rowSkill(e)
			t, ok := by[skill]
			if !ok {
				t = &skillTally{stories: map[string]bool{}}
				by[skill] = t
			}
			t.rejects++
			if namesCriterion(e) {
				continue
			}
			none++
			t.none++
			set[it.ID] = true
			t.stories[it.ID] = true
		}
	}
	if rejects == 0 {
		return NoCriterion{Count: Count{Reason: ReasonNoReject}, Stories: []string{}, BySkill: []SkillRejects{}}
	}
	out := NoCriterion{Rejects: rejects, Count: counted(none), Stories: sorted(set), BySkill: []SkillRejects{}}
	skills := make([]string, 0, len(by))
	for s := range by {
		skills = append(skills, s)
	}
	sort.Strings(skills)
	for _, s := range skills {
		t := by[s]
		out.BySkill = append(out.BySkill, SkillRejects{Skill: s, Rejects: t.rejects, NoCriterion: t.none, Stories: sorted(t.stories)})
	}
	return out
}

// namesCriterion reports whether a reject row carries a criteria entry numbered
// 1 or higher with status reject or unmet. A row with no criteria names none.
func namesCriterion(e ledger.Entry) bool {
	var p struct {
		Criteria []struct {
			Criterion int    `json:"criterion"`
			Status    string `json:"status"`
		} `json:"criteria"`
	}
	_ = json.Unmarshal(e.Payload, &p)
	for _, c := range p.Criteria {
		if c.Criterion >= 1 && (c.Status == verb.CriterionRejected || c.Status == verb.CriterionUnmet) {
			return true
		}
	}
	return false
}

func cameBack(r Returns) CameBack {
	if len(r.Created) == 0 {
		return CameBack{Count: Count{Reason: ReasonNoCreated}, Pairs: []CameBackPair{}, Stories: []string{}}
	}
	pairs := []CameBackPair{}
	set := map[string]bool{}
	for _, c := range r.Created {
		for _, id := range c.Cites {
			if done, ok := r.Done[id]; ok && c.At.After(done) {
				pairs = append(pairs, CameBackPair{Story: c.ID, Cites: id})
				set[c.ID], set[id] = true, true
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Story != pairs[j].Story {
			return pairs[i].Story < pairs[j].Story
		}
		return pairs[i].Cites < pairs[j].Cites
	})
	return CameBack{Count: counted(len(pairs)), Pairs: pairs, Stories: sorted(set)}
}

// idListCap bounds how many story ids a text line names; --json carries all.
const idListCap = 10

// IDList is how a list of story ids reads in text.
func IDList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	if len(ids) <= idListCap {
		return strings.Join(ids, ", ")
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(ids[:idListCap], ", "), len(ids)-idListCap)
}

// RatioText is how a Ratio reads in text: the value in format, or unavailable
// and why.
func RatioText(r Ratio, format string) string {
	if r.Value == nil {
		return fmt.Sprintf("unavailable (%s)", r.Reason)
	}
	return fmt.Sprintf(format, *r.Value)
}

func qualityText(w io.Writer, q QualityReport) {
	fmt.Fprintln(w, "quality:")
	passText(w, "first-pass acceptance by step", q.FirstPassByStep)
	passText(w, "first-pass acceptance by gate", q.FirstPassByGate)
	fmt.Fprintln(w, "  rejected rounds per story, by gated edge:")
	if q.Rounds.Reason != "" {
		fmt.Fprintf(w, "    unavailable (%s)\n", q.Rounds.Reason)
	}
	for _, r := range q.Rounds.Rows {
		fmt.Fprintf(w, "    %-28s mean %s  max %s  (%d stories)  at max: %s  rejected: %s\n",
			r.From+" → "+r.To, RatioText(r.Mean, "%.1f"), CountText(r.Max), r.Stories, IDList(r.AtMax), IDList(r.Rejected))
	}
	fmt.Fprintf(w, "  coder re-dispatches after an exit rejection: %s", CountText(q.Redispatches.Count))
	if q.Redispatches.Count.Value != nil {
		fmt.Fprintf(w, "  stories: %s", IDList(q.Redispatches.Stories))
	}
	fmt.Fprintln(w)
	nc := q.NoCriterion
	if nc.Count.Value == nil {
		fmt.Fprintf(w, "  rejects naming no criterion: %s\n", CountText(nc.Count))
	} else {
		fmt.Fprintf(w, "  rejects naming no criterion: %d of %d  stories: %s\n", *nc.Count.Value, nc.Rejects, IDList(nc.Stories))
	}
	for _, s := range nc.BySkill {
		fmt.Fprintf(w, "    %-36s %d of %d  stories: %s\n", s.Skill, s.NoCriterion, s.Rejects, IDList(s.Stories))
	}
	fmt.Fprintf(w, "  work that came back: %s\n", CountText(q.CameBack.Count))
	for _, p := range q.CameBack.Pairs {
		fmt.Fprintf(w, "    %s cites %s\n", p.Story, p.Cites)
	}
}

func passText(w io.Writer, label string, t PassTable) {
	fmt.Fprintf(w, "  %s:\n", label)
	if t.Reason != "" {
		fmt.Fprintf(w, "    unavailable (%s)\n", t.Reason)
	}
	for _, p := range t.Rows {
		share := p.Share
		if share.Value != nil {
			share = ratio(*share.Value * 100)
		}
		fmt.Fprintf(w, "    %-36s %d/%d (%s)  accepted: %s  rejected: %s\n",
			p.Name, p.Accepted, p.Presented, RatioText(share, "%.0f%%"), IDList(p.AcceptedStories), IDList(p.RejectedStories))
	}
}
