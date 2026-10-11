package reviewscore

import "sort"

// The verdict classification below is the one definition of a missed defect and
// a false rejection, shared by the bundled-vs-separate parity run
// (tests/reviewcorpus) and the per-binding score. Parity is NOT a reject count:
// a bundle that rejects as often as the separate sessions may still have missed
// a known defect and rejected a known-valid change to make up the number. The
// unit of comparison is a known case.
//
// It is pure — it classifies verdicts a run collected and decides nothing about
// a workflow.

// Mode is how a rubric was judged.
type Mode string

const (
	// ModeSeparate is the rubric in its own reviewer session, as today.
	ModeSeparate Mode = "separate"
	// ModeBundled is the rubric as one section of a bundled session.
	ModeBundled Mode = "bundled"
)

// Result is ONE judgement of one case in one mode. Verdict is empty when the run
// produced none for the case's own rubric (a missing verdict is not an accept).
type Result struct {
	CaseID   string
	Rubric   string
	Skill    string
	Label    Label
	Expected Verdict
	Mode     Mode
	Run      int // 1..runs per mode
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
