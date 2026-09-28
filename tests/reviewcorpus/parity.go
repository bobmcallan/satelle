package reviewcorpus

import (
	"fmt"
	"sort"
	"strings"
)

// Parity is how bundled reviewer gates (sty_23e10d92) are compared with separate
// sessions over THIS corpus. Parity is NOT a reject count: a bundle that rejects
// as often as the separate sessions may still have missed a known defect and
// rejected a known-valid change to make up the number. The unit of comparison is
// a known case, judged the same fixed number of times in each mode.
//
// This file is pure — it classifies verdicts a live run collected and decides
// nothing about a workflow; a workflow's `bundle` key is authored by hand from
// the outcome (the embedded default turns bundling on only where ParityVerdict
// says so, and stays opt-in otherwise).

// Mode is how a rubric was judged.
type Mode string

const (
	// ModeSeparate is the rubric in its own reviewer session, as today.
	ModeSeparate Mode = "separate"
	// ModeBundled is the rubric as one section of a bundled session.
	ModeBundled Mode = "bundled"
)

// ParityRuns is the fixed number of times every case is judged in each mode. It
// is a constant, not a knob: the same K for both modes is what makes the
// comparison one.
const ParityRuns = 3

// Result is ONE judgement of one case in one mode. Verdict is empty when the run
// produced none for the case's own rubric (a missing verdict is not an accept).
type Result struct {
	CaseID   string
	Rubric   string
	Skill    string
	Label    Label
	Expected Verdict
	Mode     Mode
	Run      int // 1..ParityRuns
	Verdict  Verdict
}

// MissedDefect is true when a case expected to be rejected was not: an accept,
// or no verdict at all. Silence never counts as catching a defect.
func MissedDefect(expected, got Verdict) bool {
	return expected == VerdictReject && got != VerdictReject
}

// FalseRejection is true when a known-valid case was rejected.
func FalseRejection(expected, got Verdict) bool {
	return expected == VerdictAccept && got == VerdictReject
}

// Row is one case's run, both modes side by side, with every flag derived.
type Row struct {
	CaseID   string
	Rubric   string
	Skill    string
	Label    Label
	Expected Verdict
	Run      int
	Separate Verdict
	Bundled  Verdict

	SeparateMissedDefect   bool
	BundledMissedDefect    bool
	SeparateFalseRejection bool
	BundledFalseRejection  bool
}

// Pair folds results into rows, one per case × run, ordered by rubric, case, run.
// A case-run judged in only one mode still yields a row: the absent mode's
// verdict is empty, which the parity rules read as a missed run, never a pass.
func Pair(results []Result) []Row {
	type key struct {
		caseID string
		run    int
	}
	rows := map[key]*Row{}
	var order []key
	for _, r := range results {
		k := key{r.CaseID, r.Run}
		row, ok := rows[k]
		if !ok {
			row = &Row{CaseID: r.CaseID, Rubric: r.Rubric, Skill: r.Skill, Label: r.Label, Expected: r.Expected, Run: r.Run}
			rows[k] = row
			order = append(order, k)
		}
		switch r.Mode {
		case ModeSeparate:
			row.Separate = r.Verdict
		case ModeBundled:
			row.Bundled = r.Verdict
		}
	}
	out := make([]Row, 0, len(order))
	for _, k := range order {
		r := *rows[k]
		r.SeparateMissedDefect = MissedDefect(r.Expected, r.Separate)
		r.BundledMissedDefect = MissedDefect(r.Expected, r.Bundled)
		r.SeparateFalseRejection = FalseRejection(r.Expected, r.Separate)
		r.BundledFalseRejection = FalseRejection(r.Expected, r.Bundled)
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rubric != out[j].Rubric {
			return out[i].Rubric < out[j].Rubric
		}
		if out[i].CaseID != out[j].CaseID {
			return out[i].CaseID < out[j].CaseID
		}
		return out[i].Run < out[j].Run
	})
	return out
}

// CaseSummary is one case across its runs.
type CaseSummary struct {
	CaseID   string
	Rubric   string
	Skill    string
	Label    Label
	Expected Verdict
	Runs     int

	// Caught/rejected counts per mode: a defect is CAUGHT on a run that rejects
	// it; a valid case is FALSELY REJECTED on a run that rejects it.
	SeparateCaught, BundledCaught     int
	SeparateFalseRej, BundledFalseRej int

	// RegressedDefect is how many runs the bundle caught fewer of than the
	// separate sessions did — the defect the bundle misses that separate sessions
	// caught. AddedFalseRejection is how many more false rejections the bundle
	// made than the separate sessions did. Both are zero for a case the bundle
	// judges at least as well.
	RegressedDefect     int
	AddedFalseRejection int
}

// Summarise reduces rows to one summary per case.
func Summarise(rows []Row) []CaseSummary {
	byCase := map[string]*CaseSummary{}
	var order []string
	for _, r := range rows {
		s, ok := byCase[r.CaseID]
		if !ok {
			s = &CaseSummary{CaseID: r.CaseID, Rubric: r.Rubric, Skill: r.Skill, Label: r.Label, Expected: r.Expected}
			byCase[r.CaseID] = s
			order = append(order, r.CaseID)
		}
		s.Runs++
		if r.Expected == VerdictReject {
			if !r.SeparateMissedDefect {
				s.SeparateCaught++
			}
			if !r.BundledMissedDefect {
				s.BundledCaught++
			}
		}
		if r.SeparateFalseRejection {
			s.SeparateFalseRej++
		}
		if r.BundledFalseRejection {
			s.BundledFalseRej++
		}
	}
	out := make([]CaseSummary, 0, len(order))
	for _, id := range order {
		s := *byCase[id]
		if s.SeparateCaught > s.BundledCaught {
			s.RegressedDefect = s.SeparateCaught - s.BundledCaught
		}
		if s.BundledFalseRej > s.SeparateFalseRej {
			s.AddedFalseRejection = s.BundledFalseRej - s.SeparateFalseRej
		}
		out = append(out, s)
	}
	return out
}

// ParityCorpusRoot is where a parity run reads its cases from: this package's
// own frozen corpus, and nowhere else. The parity story creates no second set,
// so a run that wants other cases has to add them HERE, before the cost change
// they judge.
func ParityCorpusRoot() string { return Dir() }

// CasesForSkills is the corpus's cases whose rubric is one of skills, in corpus
// order — the cases a comparison of those gates can judge.
func CasesForSkills(cases []Case, skills []string) []Case {
	want := map[string]bool{}
	for _, s := range skills {
		want[s] = true
	}
	var out []Case
	for _, c := range cases {
		if want[c.Skill] {
			out = append(out, c)
		}
	}
	return out
}

// Edge is one workflow edge whose gates would bundle: the edge, and EVERY
// reviewer rubric the bundle would carry. A rubric on the edge with no corpus
// case is still in the bundle, so it counts against turning bundling on.
type Edge struct {
	From, To string
	Skills   []string
}

// ParityVerdict decides whether the embedded default may turn bundling on for an
// edge. It enables only when, over the rows of the rubrics in the comparison:
//
//   - every rubric the bundle carries has at least one known defect AND one
//     known-valid case in the corpus — a rubric with no known defect cannot be
//     shown to survive bundling, so the edge stays opt-in;
//   - every case was judged ParityRuns times in each mode;
//   - the bundle missed no known defect the separate sessions caught; and
//   - the bundle added no false rejection on a known-valid case.
//
// reasons is empty exactly when enable is true; otherwise it names each thing
// that keeps the edge opt-in, so the story can record why.
func ParityVerdict(rows []Row, edge Edge) (enable bool, reasons []string) {
	inEdge := map[string]bool{}
	for _, s := range edge.Skills {
		inEdge[s] = true
	}
	sums := Summarise(rows)
	defects, valids := map[string]int{}, map[string]int{}
	for _, s := range sums {
		if !inEdge[s.Skill] {
			continue
		}
		switch s.Label {
		case LabelDefect:
			defects[s.Skill]++
		case LabelValid:
			valids[s.Skill]++
		}
		if s.Runs != ParityRuns {
			reasons = append(reasons, fmt.Sprintf("%s (%s) was judged %d time(s), want %d in each mode", s.CaseID, s.Skill, s.Runs, ParityRuns))
		}
		if s.RegressedDefect > 0 {
			reasons = append(reasons, fmt.Sprintf("the bundle missed known defect %s (%s) on %d run(s) the separate sessions caught", s.CaseID, s.Skill, s.RegressedDefect))
		}
		if s.AddedFalseRejection > 0 {
			reasons = append(reasons, fmt.Sprintf("the bundle added %d false rejection(s) on known-valid %s (%s)", s.AddedFalseRejection, s.CaseID, s.Skill))
		}
	}
	for _, skill := range edge.Skills {
		if defects[skill] == 0 {
			reasons = append(reasons, fmt.Sprintf("no known defect in the corpus for %s — the bundle cannot be shown to keep catching what that rubric catches", skill))
		}
		if valids[skill] == 0 {
			reasons = append(reasons, fmt.Sprintf("no known-valid case in the corpus for %s — a false rejection by the bundle could not be seen", skill))
		}
	}
	return len(reasons) == 0, reasons
}

// Markdown renders the per-rubric, per-run result: every case × run with both
// verdicts and both flags, then a per-case summary. Reject counts are not the
// metric and are not reported as one.
func Markdown(rows []Row) string {
	var b strings.Builder
	yn := func(v bool) string {
		if v {
			return "YES"
		}
		return "no"
	}
	verdict := func(v Verdict) string {
		if v == "" {
			return "(none)"
		}
		return string(v)
	}
	fmt.Fprintf(&b, "# Bundled vs separate reviewer sessions — parity over the frozen corpus\n\n")
	fmt.Fprintf(&b, "Every case judged %d times as separate sessions and %d times as a bundle.\n\n", ParityRuns, ParityRuns)
	b.WriteString("## Per-run results\n\n")
	b.WriteString("| case | rubric | label | expected | run | separate | bundled | missed defect (sep / bun) | false rejection (sep / bun) |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d | %s | %s | %s / %s | %s / %s |\n",
			r.CaseID, r.Rubric, r.Label, r.Expected, r.Run, verdict(r.Separate), verdict(r.Bundled),
			yn(r.SeparateMissedDefect), yn(r.BundledMissedDefect), yn(r.SeparateFalseRejection), yn(r.BundledFalseRejection))
	}
	b.WriteString("\n## Per-case summary\n\n")
	b.WriteString("| case | rubric | label | runs | caught sep / bun | false rej sep / bun | bundle missed a defect separate caught | bundle added false rejection |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, s := range Summarise(rows) {
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d / %d | %d / %d | %d | %d |\n",
			s.CaseID, s.Rubric, s.Label, s.Runs, s.SeparateCaught, s.BundledCaught,
			s.SeparateFalseRej, s.BundledFalseRej, s.RegressedDefect, s.AddedFalseRejection)
	}
	return b.String()
}
