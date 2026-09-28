package reviewcorpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sty_23e10d92 AC7/AC8/AC10: the parity harness classifies verdicts a live run
// collected. These tests pin the classification and the enable decision with
// canned verdicts, so they run in `go test ./...` without spending anything.

func res(caseID, skill string, label Label, mode Mode, run int, v Verdict) Result {
	exp := VerdictAccept
	if label == LabelDefect {
		exp = VerdictReject
	}
	return Result{CaseID: caseID, Rubric: skill, Skill: skill, Label: label, Expected: exp, Mode: mode, Run: run, Verdict: v}
}

// runs builds K results for one case and mode from a verdict per run.
func runs(caseID, skill string, label Label, mode Mode, verdicts ...Verdict) []Result {
	var out []Result
	for i, v := range verdicts {
		out = append(out, res(caseID, skill, label, mode, i+1, v))
	}
	return out
}

func concat(parts ...[]Result) []Result {
	var out []Result
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestClassifyMissedDefectAndFalseRejection(t *testing.T) {
	cases := []struct {
		name            string
		expected, got   Verdict
		missed, falseRj bool
	}{
		{"defect caught", VerdictReject, VerdictReject, false, false},
		{"defect accepted is missed", VerdictReject, VerdictAccept, true, false},
		{"defect with no verdict is missed", VerdictReject, "", true, false},
		{"valid accepted", VerdictAccept, VerdictAccept, false, false},
		{"valid rejected is a false rejection", VerdictAccept, VerdictReject, false, true},
		{"valid with no verdict is neither", VerdictAccept, "", false, false},
	}
	for _, tc := range cases {
		if got := MissedDefect(tc.expected, tc.got); got != tc.missed {
			t.Errorf("%s: MissedDefect = %v, want %v", tc.name, got, tc.missed)
		}
		if got := FalseRejection(tc.expected, tc.got); got != tc.falseRj {
			t.Errorf("%s: FalseRejection = %v, want %v", tc.name, got, tc.falseRj)
		}
	}
}

func TestPairFlagsEachRunPerMode(t *testing.T) {
	rs := concat(
		runs("d1", "skill-a", LabelDefect, ModeSeparate, VerdictReject, VerdictReject, VerdictAccept),
		runs("d1", "skill-a", LabelDefect, ModeBundled, VerdictReject, VerdictAccept, VerdictAccept),
	)
	rows := Pair(rs)
	if len(rows) != 3 {
		t.Fatalf("one row per case-run, got %d", len(rows))
	}
	wantSep := []bool{false, false, true}
	wantBun := []bool{false, true, true}
	for i, r := range rows {
		if r.Run != i+1 || r.SeparateMissedDefect != wantSep[i] || r.BundledMissedDefect != wantBun[i] {
			t.Errorf("row %d = %+v", i, r)
		}
	}
	md := Markdown(rows)
	for _, want := range []string{"| d1 |", "missed defect (sep / bun)", "false rejection (sep / bun)", "YES"} {
		if !strings.Contains(md, want) {
			t.Errorf("report must carry per-run flags; missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(strings.ToLower(md), "reject count") {
		t.Error("the report must not present reject counts as the metric")
	}
}

var edgeAB = Edge{From: "backlog", To: "plan", Skills: []string{"skill-a", "skill-b"}}

// fullCorpusRows is a comparison covering both rubrics with a defect and a valid
// case each, the bundle judging identically to the separate sessions.
func fullCorpusRows() []Result {
	return concat(
		runs("a-def", "skill-a", LabelDefect, ModeSeparate, VerdictReject, VerdictReject, VerdictReject),
		runs("a-def", "skill-a", LabelDefect, ModeBundled, VerdictReject, VerdictReject, VerdictReject),
		runs("a-ok", "skill-a", LabelValid, ModeSeparate, VerdictAccept, VerdictAccept, VerdictAccept),
		runs("a-ok", "skill-a", LabelValid, ModeBundled, VerdictAccept, VerdictAccept, VerdictAccept),
		runs("b-def", "skill-b", LabelDefect, ModeSeparate, VerdictReject, VerdictReject, VerdictAccept),
		runs("b-def", "skill-b", LabelDefect, ModeBundled, VerdictReject, VerdictReject, VerdictReject),
		runs("b-ok", "skill-b", LabelValid, ModeSeparate, VerdictAccept, VerdictReject, VerdictAccept),
		runs("b-ok", "skill-b", LabelValid, ModeBundled, VerdictAccept, VerdictAccept, VerdictAccept),
	)
}

func TestParityVerdictEnablesWhenTheBundleIsAsGoodOnEveryKnownCase(t *testing.T) {
	// The bundle here is STRICTLY better on two cases; better is still parity.
	enable, reasons := ParityVerdict(Pair(fullCorpusRows()), edgeAB)
	if !enable || len(reasons) != 0 {
		t.Fatalf("want enable, got %v", reasons)
	}
}

func TestParityVerdictRefusesAMissedDefect(t *testing.T) {
	rs := fullCorpusRows()
	// a-def: separate caught all three; the bundle misses one.
	for i := range rs {
		if rs[i].CaseID == "a-def" && rs[i].Mode == ModeBundled && rs[i].Run == 2 {
			rs[i].Verdict = VerdictAccept
		}
	}
	enable, reasons := ParityVerdict(Pair(rs), edgeAB)
	if enable {
		t.Fatal("a defect the separate sessions caught and the bundle missed must keep the edge opt-in")
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "a-def") {
		t.Errorf("the reason must name the case: %v", reasons)
	}
}

func TestParityVerdictRefusesAnAddedFalseRejection(t *testing.T) {
	rs := fullCorpusRows()
	for i := range rs {
		if rs[i].CaseID == "a-ok" && rs[i].Mode == ModeBundled && rs[i].Run == 3 {
			rs[i].Verdict = VerdictReject
		}
	}
	enable, reasons := ParityVerdict(Pair(rs), edgeAB)
	if enable || !strings.Contains(strings.Join(reasons, "\n"), "a-ok") {
		t.Fatalf("an added false rejection must keep the edge opt-in: %v", reasons)
	}
}

func TestParityVerdictIsNotARejectCount(t *testing.T) {
	// Make the bundle's TOTAL reject count equal the separate sessions': it misses
	// a-def once (a defect the separate sessions caught on all three runs) and
	// makes up the count with a reject on the known-valid b-ok run that separate
	// accepted. The totals match; parity must still refuse, on the missed defect.
	rs := fullCorpusRows()
	for i := range rs {
		if rs[i].Mode != ModeBundled {
			continue
		}
		switch {
		case rs[i].CaseID == "a-def" && rs[i].Run == 1:
			rs[i].Verdict = VerdictAccept
		case rs[i].CaseID == "b-ok" && rs[i].Run == 1:
			rs[i].Verdict = VerdictReject
		}
	}
	var sepRejects, bunRejects int
	for _, r := range rs {
		if r.Verdict != VerdictReject {
			continue
		}
		if r.Mode == ModeSeparate {
			sepRejects++
		} else {
			bunRejects++
		}
	}
	// fullCorpusRows' separate mode rejects 3+2+1=6 times... and so does the bundle
	// here (a-def 2, b-def 3, b-ok 1 → 6).
	if sepRejects != bunRejects {
		t.Fatalf("test setup: reject totals must be equal, got separate %d / bundled %d", sepRejects, bunRejects)
	}
	enable, reasons := ParityVerdict(Pair(rs), edgeAB)
	if enable {
		t.Fatalf("equal reject totals must not hide a missed defect: %v", reasons)
	}
}

func TestParityVerdictNeedsADefectAndAValidCasePerRubric(t *testing.T) {
	// skill-b has no known defect: the bundle cannot be shown to keep catching it.
	rs := concat(
		runs("a-def", "skill-a", LabelDefect, ModeSeparate, VerdictReject, VerdictReject, VerdictReject),
		runs("a-def", "skill-a", LabelDefect, ModeBundled, VerdictReject, VerdictReject, VerdictReject),
		runs("a-ok", "skill-a", LabelValid, ModeSeparate, VerdictAccept, VerdictAccept, VerdictAccept),
		runs("a-ok", "skill-a", LabelValid, ModeBundled, VerdictAccept, VerdictAccept, VerdictAccept),
		runs("b-ok", "skill-b", LabelValid, ModeSeparate, VerdictAccept, VerdictAccept, VerdictAccept),
		runs("b-ok", "skill-b", LabelValid, ModeBundled, VerdictAccept, VerdictAccept, VerdictAccept),
	)
	enable, reasons := ParityVerdict(Pair(rs), edgeAB)
	if enable {
		t.Fatal("a rubric with no known defect keeps the edge opt-in")
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "no known defect in the corpus for skill-b") {
		t.Errorf("the reason must say which rubric has no defect: %v", reasons)
	}
}

func TestParityVerdictCountsARubricWithNoCorpusAtAll(t *testing.T) {
	// The bundle carries skill-c too, and the corpus has nothing on it.
	edge := Edge{Skills: []string{"skill-a", "skill-b", "skill-c"}}
	enable, reasons := ParityVerdict(Pair(fullCorpusRows()), edge)
	if enable {
		t.Fatal("an uncovered rubric in the bundle keeps the edge opt-in")
	}
	joined := strings.Join(reasons, "\n")
	if !strings.Contains(joined, "skill-c") {
		t.Errorf("the reason must name skill-c: %v", reasons)
	}
}

func TestParityVerdictRequiresTheFixedRunCountInEachMode(t *testing.T) {
	rs := fullCorpusRows()
	var trimmed []Result
	for _, r := range rs {
		if r.CaseID == "b-ok" && r.Run == 3 { // one run dropped from b-ok
			continue
		}
		trimmed = append(trimmed, r)
	}
	enable, reasons := ParityVerdict(Pair(trimmed), edgeAB)
	if enable || !strings.Contains(strings.Join(reasons, "\n"), "b-ok") {
		t.Fatalf("an unequal run count is not a comparison: %v", reasons)
	}
}

func TestParityMissingBundledRunIsAMissedDefectNotAPass(t *testing.T) {
	rs := concat(
		runs("a-def", "skill-a", LabelDefect, ModeSeparate, VerdictReject, VerdictReject, VerdictReject),
		// the bundled mode produced no verdict on run 3
		runs("a-def", "skill-a", LabelDefect, ModeBundled, VerdictReject, VerdictReject, ""),
	)
	rows := Pair(rs)
	if !rows[2].BundledMissedDefect || rows[2].SeparateMissedDefect {
		t.Errorf("silence is a miss: %+v", rows[2])
	}
}

// TestParityHarnessReadsOnlyTheFrozenCorpus is AC10: the harness's case source is
// this package's own corpus, and this story added no testdata beside it.
func TestParityHarnessReadsOnlyTheFrozenCorpus(t *testing.T) {
	if ParityCorpusRoot() != Dir() {
		t.Fatalf("harness case source = %q, want the corpus at %q", ParityCorpusRoot(), Dir())
	}
	entries, err := os.ReadDir(filepath.Dir(Dir()))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "corpus" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("testdata must hold only the one corpus, found %v", names)
	}
	cases, err := Load(ParityCorpusRoot())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.ID] {
			t.Errorf("case id %q is not unique — parity keys results by case id", c.ID)
		}
		seen[c.ID] = true
	}
}
