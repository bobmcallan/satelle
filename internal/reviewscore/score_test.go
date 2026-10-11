package reviewscore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCorpus lays out a corpus directory of cases: rubric/id/{case.json,change.diff}.
func writeCorpus(t *testing.T, root string, cases ...string) {
	t.Helper()
	for _, c := range cases {
		parts := strings.SplitN(c, "|", 3) // rubric|id|case.json body
		dir := filepath.Join(root, parts[0], parts[1])
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "case.json"), []byte(parts[2]), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "change.diff"), []byte("+change for "+parts[1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const (
	defectMarked   = `{"id":"%ID%","rubric":"%R%","skill":"skill-%R%","label":"defect","story_id":"sty_x","expected_verdict":"reject","defect_markers":["missing retry"],"summary":"s"}`
	defectUnmarked = `{"id":"%ID%","rubric":"%R%","skill":"skill-%R%","label":"defect","story_id":"sty_x","expected_verdict":"reject","summary":"s"}`
	validCase      = `{"id":"%ID%","rubric":"%R%","skill":"skill-%R%","label":"valid","story_id":"sty_x","expected_verdict":"accept","summary":"s"}`
)

func spec(rubric, id, tmpl string) string {
	return rubric + "|" + id + "|" + strings.NewReplacer("%ID%", id, "%R%", rubric).Replace(tmpl)
}

// stubCorpus: rubric "a" has a marked defect, an unmarked defect and a valid
// case; rubric "b" has a marked defect and a valid case.
func stubCorpus(t *testing.T) (string, []Case) {
	t.Helper()
	root := t.TempDir()
	writeCorpus(t, root,
		spec("a", "a-defect-marked", defectMarked),
		spec("a", "a-defect-unmarked", defectUnmarked),
		spec("a", "a-valid", validCase),
		spec("b", "b-defect-marked", defectMarked),
		spec("b", "b-valid", validCase),
	)
	cases, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, cases
}

// stub answers per case id: verdict, notes and (optionally) usage.
type stub map[string]JudgeResult

func (s stub) Judge(_ context.Context, c Case) (JudgeResult, error) { return s[c.ID], nil }

func rubric(t *testing.T, r Report, name string) Metrics {
	t.Helper()
	for _, rm := range r.ByRubric {
		if rm.Rubric == name {
			return rm.Metrics
		}
	}
	t.Fatalf("no rubric %q in report", name)
	return Metrics{}
}

func TestScoreCountsRecallFalseBlockersEscapesAndFindingMatchPerRubric(t *testing.T) {
	_, cases := stubCorpus(t)
	one := 0.01
	rep := Score(context.Background(), cases, stub{
		// a: marked defect rejected citing the marker; unmarked defect accepted
		// (an escape); valid case rejected (a false blocker).
		"a-defect-marked":   {Verdict: VerdictReject, Notes: "The change has a MISSING RETRY on timeout", Usage: Usage{Available: true, TokensIn: 100, TokensOut: 10, CostUSD: &one}, Wall: 2 * time.Second},
		"a-defect-unmarked": {Verdict: VerdictAccept, Notes: "fine", Usage: Usage{Available: true, TokensIn: 50, TokensOut: 5, CostUSD: &one}, Wall: time.Second},
		"a-valid":           {Verdict: VerdictReject, Notes: "nit", Usage: Usage{Available: true, TokensIn: 20, TokensOut: 2, CostUSD: &one}, Wall: time.Second},
		// b: marked defect rejected for a different reason (caught, finding
		// missed); valid accepted.
		"b-defect-marked": {Verdict: VerdictReject, Notes: "style", Usage: Usage{Available: true, TokensIn: 1, TokensOut: 1, CostUSD: &one}, Wall: time.Second},
		"b-valid":         {Verdict: VerdictAccept, Notes: "ok", Usage: Usage{Available: true, TokensIn: 1, TokensOut: 1, CostUSD: &one}, Wall: time.Second},
	}, Options{Binding: "stub", Adapter: "stubcli"})

	a := rubric(t, rep, "a")
	if a.Defect != 2 || a.Caught != 1 || a.Escapes != 1 || a.FalseBlockers != 1 || a.Valid != 1 {
		t.Errorf("rubric a = %+v", a)
	}
	if a.FindingScored != 1 || a.FindingMatched != 1 || a.FindingUnscored != 1 {
		t.Errorf("rubric a finding = scored %d matched %d unscored %d", a.FindingScored, a.FindingMatched, a.FindingUnscored)
	}
	b := rubric(t, rep, "b")
	if b.Caught != 1 || b.Escapes != 0 || b.FalseBlockers != 0 || b.FindingScored != 1 || b.FindingMatched != 0 {
		t.Errorf("rubric b = %+v", b)
	}
	tot := rep.Total
	if tot.Caught != 2 || tot.Escapes != 1 || tot.FalseBlockers != 1 || tot.Defect != 3 || tot.Valid != 2 {
		t.Errorf("total = %+v", tot)
	}
	if tot.WallMs != 6000 || tot.TokensIn != 172 || tot.TokensOut != 19 || tot.UsageRuns != 5 {
		t.Errorf("total cost = %+v", tot)
	}
	if got := tot.Cell("recall"); !strings.HasPrefix(got, "2/3") {
		t.Errorf("recall cell = %q", got)
	}
	if got := a.Cell("finding match"); !strings.HasPrefix(got, "1/1") || !strings.Contains(got, "unscored") {
		t.Errorf("finding cell = %q", got)
	}
	if rep.Cases[0].Source != OriginCorpus {
		t.Errorf("a corpus row must say so: %+v", rep.Cases[0])
	}
}

func TestScoreNeverPrintsUnreportedUsageAsZero(t *testing.T) {
	_, cases := stubCorpus(t)
	judge := JudgeFunc(func(_ context.Context, c Case) (JudgeResult, error) {
		return JudgeResult{Verdict: VerdictAccept, Usage: Usage{Reason: "unavailable (stubcli: the stub CLI emits plain text)"}}, nil
	})
	rep := Score(context.Background(), cases, judge, Options{Binding: "stub", Adapter: "stubcli"})
	var out bytes.Buffer
	rep.Render(&out)
	text := out.String()
	if !strings.Contains(text, "unavailable (stubcli: the stub CLI emits plain text)") {
		t.Errorf("the adapter-named reason must be printed:\n%s", text)
	}
	if strings.Contains(text, "0 in / 0 out") || strings.Contains(text, "$0.0000") {
		t.Errorf("unreported usage printed as a measured zero:\n%s", text)
	}
	if rep.Total.UsageRuns != 0 {
		t.Errorf("UsageRuns = %d, want 0", rep.Total.UsageRuns)
	}
}

func TestScoreRendersATablePerRubricAndTotalAndWritesJSON(t *testing.T) {
	_, cases := stubCorpus(t)
	rep := Score(context.Background(), cases, stub{
		"a-defect-marked": {Verdict: VerdictReject, Notes: "missing retry"},
	}, Options{Binding: "stub", Adapter: "stubcli"})
	var out bytes.Buffer
	rep.Render(&out)
	text := out.String()
	for _, want := range []string{"RECALL", "FALSE BLOCKERS", "ESCAPES", "FINDING MATCH", "WALL TIME", "TOKENS", "COST", "TOTAL", "binding stub", "adapter stubcli"} {
		if !strings.Contains(text, want) {
			t.Errorf("table missing %q:\n%s", want, text)
		}
	}
	for _, line := range []string{"\na ", "\nb "} {
		if !strings.Contains(text, line) {
			t.Errorf("table has no row for %q:\n%s", strings.TrimSpace(line), text)
		}
	}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := rep.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	back, err := ReadReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.CaseDigest != rep.CaseDigest || back.Total.Caught != rep.Total.Caught || len(back.Cases) != len(rep.Cases) {
		t.Errorf("round trip lost data: %+v vs %+v", back.Total, rep.Total)
	}
}

func TestScoreJudgeErrorLeavesNoVerdictAndNeverCountsAsACatch(t *testing.T) {
	_, cases := stubCorpus(t)
	judge := JudgeFunc(func(_ context.Context, c Case) (JudgeResult, error) {
		return JudgeResult{}, context.DeadlineExceeded
	})
	rep := Score(context.Background(), cases, judge, Options{Binding: "stub"})
	if rep.Total.Caught != 0 || rep.Total.NoVerdict != 5 || rep.Total.Escapes != 0 {
		t.Errorf("total = %+v", rep.Total)
	}
	if rep.Cases[0].Error == "" {
		t.Error("the judge error must ride on the row")
	}
}

func TestScoreRunsJudgesEachCaseThatManyTimes(t *testing.T) {
	_, cases := stubCorpus(t)
	rep := Score(context.Background(), cases, stub{}, Options{Binding: "stub", Runs: 3, Workers: 4})
	if rep.Total.Rows != 15 || rep.Runs != 3 {
		t.Errorf("rows = %d runs = %d", rep.Total.Rows, rep.Runs)
	}
}

func TestLoadedCorpusAndReplayAreScoredTogetherAndTagged(t *testing.T) {
	_, corpus := stubCorpus(t)
	replayDir := t.TempDir()
	if _, err := Capture(captureFixture(t, replayDir, VerdictReject, []string{"no acceptance"})); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(replayDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != 1 || replay[0].Origin != OriginReplay || replay[0].PayloadSource != PayloadReconstructed {
		t.Fatalf("replay = %+v", replay)
	}
	all := append(append([]Case{}, corpus...), replay...)
	rep := Score(context.Background(), all, stub{
		replay[0].ID: {Verdict: VerdictReject, Notes: "There is NO ACCEPTANCE criteria"},
	}, Options{Binding: "stub"})
	var found bool
	for _, r := range rep.Cases {
		if r.ID == replay[0].ID {
			found = true
			if r.Source != OriginReplay || r.PayloadSource != PayloadReconstructed || r.FindingMatch != FindingMatched || r.Outcome != OutcomeCaught {
				t.Errorf("replay row = %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("the replay case was not scored")
	}
	if rep.CaseDigest == CaseSetDigest(corpus) {
		t.Error("adding a replay case must change the case digest")
	}
}

// A replay that lacks material its recorded reviewer judged measures the capture,
// not the binding: it is neither judged nor counted, and the report names its gaps.
func TestScoreSkipsUnfaithfulReplaysAndReportsTheirGaps(t *testing.T) {
	_, corpus := stubCorpus(t)
	in := captureFixture(t, t.TempDir(), VerdictReject, []string{"no acceptance"})
	in.Entries = append(in.Entries, attached("evt_reatt", "plan"))
	if _, err := Capture(in); err != nil {
		t.Fatal(err)
	}
	replay, err := LoadReplay(in.OutDir)
	if err != nil || len(replay) != 1 {
		t.Fatalf("replay = %v, %v", replay, err)
	}
	var judged []string
	judge := JudgeFunc(func(_ context.Context, c Case) (JudgeResult, error) {
		judged = append(judged, c.ID)
		return JudgeResult{Verdict: VerdictAccept}, nil // would be an escape on the replay
	})
	rep := Score(context.Background(), append(append([]Case{}, corpus...), replay...), judge, Options{Binding: "stub"})
	for _, id := range judged {
		if id == replay[0].ID {
			t.Fatal("an unfaithful replay must not reach the reviewer")
		}
	}
	if rep.Total.Unfaithful != 1 || rep.Total.Rows != len(corpus) {
		t.Errorf("total = %+v", rep.Total)
	}
	if rep.Total.Escapes != 3 { // the three corpus defects the stub accepts — never the replay's
		t.Errorf("escapes = %d, want only the corpus defects", rep.Total.Escapes)
	}
	// A rubric whose only case was skipped has nothing measured to show.
	if m := rubric(t, rep, replay[0].Rubric); m.Rows != 0 || m.Cell("tokens") != "n/a" || m.Cell("cost") != "n/a" || m.Cell("wall time") != "n/a" {
		t.Errorf("rubric with only an unfaithful case = %+v", m)
	}
	var buf bytes.Buffer
	rep.Render(&buf)
	text := buf.String()
	if !strings.Contains(text, "1 unfaithful replay case-run(s)") || !strings.Contains(text, "re-attached after the review") {
		t.Errorf("render must report the unfaithful case and why:\n%s", text)
	}
	var row *RowResult
	for i := range rep.Cases {
		if rep.Cases[i].ID == replay[0].ID {
			row = &rep.Cases[i]
		}
	}
	if row == nil || row.Outcome != OutcomeUnfaithful || len(row.PayloadGaps) == 0 {
		t.Errorf("row = %+v", row)
	}
}

// Tokens without a cost must carry the adapter's own reason into the row and the
// cost cell, not the generic fallback.
func TestScoreCarriesTheAdapterNamedCostReasonWhenTokensCameWithoutCost(t *testing.T) {
	_, cases := stubCorpus(t)
	const reason = "pi: the model is unpriced"
	judge := JudgeFunc(func(_ context.Context, c Case) (JudgeResult, error) {
		return JudgeResult{Verdict: VerdictReject, Usage: Usage{Available: true, TokensIn: 10, TokensOut: 2, CostReason: reason}}, nil
	})
	rep := Score(context.Background(), cases, judge, Options{Binding: "stub", Adapter: "pi"})
	for _, r := range rep.Cases {
		if r.CostUnavailable != reason || r.CostUSD != nil || r.UsageUnavailable != "" {
			t.Fatalf("row = %+v", r)
		}
	}
	if got := rep.Total.Cell("cost"); got != reason {
		t.Errorf("cost cell = %q, want the adapter's reason", got)
	}
	if got := rep.Total.Cell("tokens"); !strings.Contains(got, "50 in / 10 out") {
		t.Errorf("tokens cell = %q", got)
	}
	b, _ := json.Marshal(rep.Cases[0])
	if !strings.Contains(string(b), `"cost_unavailable":"pi: the model is unpriced"`) {
		t.Errorf("the JSON row must carry the reason: %s", b)
	}
	var buf bytes.Buffer
	rep.Render(&buf)
	if !strings.Contains(buf.String(), reason) {
		t.Errorf("the printed table must name the reason:\n%s", buf.String())
	}
}
