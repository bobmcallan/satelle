package reviewscore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Usage is what a judge run reported about its own cost. Available is false when
// the adapter reports none, and Reason then names the adapter and why — an
// unreported figure is never a measured zero.
type Usage struct {
	Available bool
	TokensIn  int
	TokensOut int
	CostUSD   *float64
	// Reason is why no usage at all was reported; CostReason why tokens were
	// reported without a cost.
	Reason     string
	CostReason string
}

// JudgeResult is one reviewer judgement of one case. Verdict is empty when the
// run produced none.
type JudgeResult struct {
	Verdict Verdict
	Notes   string
	Model   string
	Usage   Usage
	Wall    time.Duration
}

// Judge runs a reviewer binding over one case. The production judge is
// agentstep.JudgeCase, which goes through the same isolated reviewer path a real
// gate uses; tests substitute a stub.
type Judge interface {
	Judge(ctx context.Context, c Case) (JudgeResult, error)
}

// JudgeFunc adapts a function to a Judge.
type JudgeFunc func(ctx context.Context, c Case) (JudgeResult, error)

// Judge implements Judge.
func (f JudgeFunc) Judge(ctx context.Context, c Case) (JudgeResult, error) { return f(ctx, c) }

// Options are the run parameters of one score.
type Options struct {
	Binding string
	// Adapter names the agent CLI adapter behind the binding, recorded on the
	// report so a comparison states what it compared.
	Adapter string
	// Runs is how many times each case is judged (default 1); Workers bounds
	// concurrent judgements (default 1).
	Runs    int
	Workers int
}

// Finding match states of one row.
const (
	FindingMatched  = "matched"  // a rejected defect whose notes cite a defect marker
	FindingMissed   = "missed"   // a defect with markers whose notes cite none, or that was not rejected
	FindingUnscored = "unscored" // a defect that declares no markers
	FindingNA       = "n/a"      // a valid case: there is no defect to cite
)

// Outcome names what the reviewer did with a case.
const (
	OutcomeCaught       = "caught"        // a defect rejected
	OutcomeEscape       = "escape"        // a defect accepted
	OutcomeNoVerdict    = "no_verdict"    // no verdict at all (a defect so judged was not caught either)
	OutcomeFalseBlocker = "false_blocker" // a valid case rejected
	OutcomeOK           = "ok"            // a valid case accepted
	// OutcomeUnfaithful marks a replay case that lacks material its recorded
	// reviewer judged: it is not judged and counts in no rate.
	OutcomeUnfaithful = "unfaithful"
)

// RowResult is one judged case-run.
type RowResult struct {
	ID               string   `json:"id"`
	Rubric           string   `json:"rubric"`
	Skill            string   `json:"skill"`
	Source           Origin   `json:"source"`
	PayloadSource    string   `json:"payload_source,omitempty"`
	Run              int      `json:"run"`
	Label            Label    `json:"label"`
	Expected         Verdict  `json:"expected"`
	Got              Verdict  `json:"got,omitempty"`
	Outcome          string   `json:"outcome"`
	Notes            string   `json:"notes,omitempty"`
	FindingMatch     string   `json:"finding_match"`
	WallMs           int64    `json:"wall_ms"`
	TokensIn         int      `json:"tokens_in,omitempty"`
	TokensOut        int      `json:"tokens_out,omitempty"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
	UsageUnavailable string   `json:"usage_unavailable,omitempty"`
	CostUnavailable  string   `json:"cost_unavailable,omitempty"`
	PayloadGaps      []string `json:"payload_gaps,omitempty"`
	Model            string   `json:"model_resolved,omitempty"`
	Error            string   `json:"error,omitempty"`
}

// Metrics aggregates rows. Defect and Valid count case-runs, so Recall is
// Caught/Defect, the false-blocker rate is FalseBlockers/Valid and the escape
// rate is Escapes/Defect.
type Metrics struct {
	Rows int `json:"rows"`
	// Unfaithful counts the case-runs skipped because the replay lacks material
	// its recorded reviewer judged; they are in no other figure.
	Unfaithful    int `json:"unfaithful,omitempty"`
	Defect        int `json:"defect"`
	Valid         int `json:"valid"`
	Caught        int `json:"caught"`
	Escapes       int `json:"escapes"`
	NoVerdict     int `json:"no_verdict"`
	FalseBlockers int `json:"false_blockers"`

	// FindingScored is the defect case-runs that declare markers; FindingMatched
	// the ones rejected with a note that cites one. FindingUnscored counts the
	// defect case-runs with no markers.
	FindingScored   int `json:"finding_scored"`
	FindingMatched  int `json:"finding_matched"`
	FindingUnscored int `json:"finding_unscored"`

	WallMs int64 `json:"wall_ms"`
	// UsageRuns/CostRuns count the rows whose adapter reported tokens/cost, so a
	// total built from fewer rows than Rows says so instead of passing as whole.
	UsageRuns          int      `json:"usage_runs"`
	CostRuns           int      `json:"cost_runs"`
	TokensIn           int      `json:"tokens_in"`
	TokensOut          int      `json:"tokens_out"`
	CostUSD            float64  `json:"cost_usd"`
	UsageUnavailableBy []string `json:"usage_unavailable,omitempty"`
	// CostUnavailableBy are the adapter-named reasons tokens came without a cost.
	CostUnavailableBy []string `json:"cost_unavailable,omitempty"`
}

// RubricMetrics is the metrics of one rubric.
type RubricMetrics struct {
	Rubric  string  `json:"rubric"`
	Metrics Metrics `json:"metrics"`
}

// Report is the JSON result of scoring one binding.
type Report struct {
	Binding       string          `json:"binding"`
	Adapter       string          `json:"adapter,omitempty"`
	ModelResolved []string        `json:"model_resolved,omitempty"`
	Runs          int             `json:"runs"`
	CaseDigest    string          `json:"case_digest"`
	CaseIDs       []string        `json:"case_ids"`
	Cases         []RowResult     `json:"cases"`
	ByRubric      []RubricMetrics `json:"by_rubric"`
	Total         Metrics         `json:"total"`
}

// caseKey is the identity of a case across reports.
func caseKey(c Case) string { return string(c.Origin) + ":" + c.Rubric + "/" + c.ID }

// CaseSetDigest is a sha256 over the sorted case identities and their frozen
// bytes, so two reports can tell whether they judged the same cases.
func CaseSetDigest(cases []Case) string {
	lines := make([]string, 0, len(cases))
	for _, c := range cases {
		d := c.Digest
		if d == "" {
			b, _ := json.Marshal(c)
			sum := sha256.Sum256(append(b, c.Diff...))
			d = hex.EncodeToString(sum[:])
		}
		lines = append(lines, caseKey(c)+"@"+d)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// Score judges every case Runs times with judge and aggregates the result per
// rubric and in total. A judge error leaves that row without a verdict (and the
// error on the row); it never aborts the score. Classification goes through
// MissedDefect and FalseRejection, the same functions the parity run uses.
func Score(ctx context.Context, cases []Case, judge Judge, opts Options) Report {
	runs, workers := opts.Runs, opts.Workers
	if runs < 1 {
		runs = 1
	}
	if workers < 1 {
		workers = 1
	}
	type task struct {
		c   Case
		run int
	}
	var tasks []task
	for _, c := range cases {
		for r := 1; r <= runs; r++ {
			tasks = append(tasks, task{c, r})
		}
	}
	rows := make([]RowResult, len(tasks))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t task) {
			defer wg.Done()
			defer func() { <-sem }()
			rows[i] = judgeRow(ctx, judge, t.c, t.run)
		}(i, t)
	}
	wg.Wait()

	rep := Report{Binding: opts.Binding, Adapter: opts.Adapter, Runs: runs, CaseDigest: CaseSetDigest(cases), Cases: rows}
	for _, c := range cases {
		rep.CaseIDs = append(rep.CaseIDs, caseKey(c))
	}
	sort.Strings(rep.CaseIDs)
	byRubric := map[string][]RowResult{}
	models := map[string]bool{}
	for _, r := range rows {
		byRubric[r.Rubric] = append(byRubric[r.Rubric], r)
		if r.Model != "" {
			models[r.Model] = true
		}
	}
	for m := range models {
		rep.ModelResolved = append(rep.ModelResolved, m)
	}
	sort.Strings(rep.ModelResolved)
	var rubrics []string
	for r := range byRubric {
		rubrics = append(rubrics, r)
	}
	sort.Strings(rubrics)
	for _, r := range rubrics {
		rep.ByRubric = append(rep.ByRubric, RubricMetrics{Rubric: r, Metrics: Aggregate(byRubric[r])})
	}
	rep.Total = Aggregate(rows)
	return rep
}

func judgeRow(ctx context.Context, judge Judge, c Case, run int) RowResult {
	row := RowResult{
		ID: c.ID, Rubric: c.Rubric, Skill: c.Skill, Source: c.Origin, PayloadSource: c.PayloadSource,
		Run: run, Label: c.Label, Expected: c.ExpectedVerdict,
	}
	if c.Unfaithful() {
		row.Outcome, row.FindingMatch, row.PayloadGaps = OutcomeUnfaithful, FindingNA, c.PayloadGaps
		return row
	}
	start := time.Now()
	res, err := judge.Judge(ctx, c)
	row.WallMs = res.Wall.Milliseconds()
	if res.Wall == 0 {
		row.WallMs = time.Since(start).Milliseconds()
	}
	if err != nil {
		row.Error = err.Error()
	} else {
		row.Got = res.Verdict
	}
	row.Notes = res.Notes
	row.Model = res.Model
	if res.Usage.Available {
		row.TokensIn, row.TokensOut, row.CostUSD = res.Usage.TokensIn, res.Usage.TokensOut, res.Usage.CostUSD
		if row.CostUSD == nil {
			row.CostUnavailable = res.Usage.CostReason
			if row.CostUnavailable == "" {
				row.CostUnavailable = "unavailable (the adapter reported no cost)"
			}
		}
	} else {
		row.UsageUnavailable = res.Usage.Reason
		if row.UsageUnavailable == "" {
			row.UsageUnavailable = "unavailable (the judge reported no usage)"
		}
	}
	row.Outcome = classify(c.ExpectedVerdict, row.Got)
	row.FindingMatch = findingMatch(c, row.Got, row.Notes)
	return row
}

func classify(expected, got Verdict) string {
	switch {
	case MissedDefect(expected, got):
		if got == VerdictAccept {
			return OutcomeEscape
		}
		return OutcomeNoVerdict
	case FalseRejection(expected, got):
		return OutcomeFalseBlocker
	case expected == VerdictReject:
		return OutcomeCaught
	case got == "":
		return OutcomeNoVerdict
	}
	return OutcomeOK
}

// findingMatch reports whether the reviewer's notes cite the case's known
// defect: a defect that declares markers is matched when it was rejected and the
// notes contain one of them (case-insensitive).
func findingMatch(c Case, got Verdict, notes string) string {
	if c.ExpectedVerdict != VerdictReject {
		return FindingNA
	}
	if len(c.DefectMarkers) == 0 {
		return FindingUnscored
	}
	if got != VerdictReject {
		return FindingMissed
	}
	low := strings.ToLower(notes)
	for _, m := range c.DefectMarkers {
		if m = strings.TrimSpace(m); m != "" && strings.Contains(low, strings.ToLower(m)) {
			return FindingMatched
		}
	}
	return FindingMissed
}

// Aggregate folds rows into metrics.
func Aggregate(rows []RowResult) Metrics {
	var m Metrics
	reasons, costReasons := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		if r.Outcome == OutcomeUnfaithful {
			m.Unfaithful++
			continue
		}
		m.Rows++
		if r.Expected == VerdictReject {
			m.Defect++
		} else {
			m.Valid++
		}
		switch r.Outcome {
		case OutcomeCaught:
			m.Caught++
		case OutcomeEscape:
			m.Escapes++
		case OutcomeNoVerdict:
			m.NoVerdict++
		case OutcomeFalseBlocker:
			m.FalseBlockers++
		}
		switch r.FindingMatch {
		case FindingMatched:
			m.FindingScored++
			m.FindingMatched++
		case FindingMissed:
			m.FindingScored++
		case FindingUnscored:
			m.FindingUnscored++
		}
		m.WallMs += r.WallMs
		if r.UsageUnavailable == "" {
			m.UsageRuns++
			m.TokensIn += r.TokensIn
			m.TokensOut += r.TokensOut
			if r.CostUSD != nil {
				m.CostRuns++
				m.CostUSD += *r.CostUSD
			} else if r.CostUnavailable != "" {
				costReasons[r.CostUnavailable] = true
			}
		} else {
			reasons[r.UsageUnavailable] = true
		}
	}
	for r := range reasons {
		m.UsageUnavailableBy = append(m.UsageUnavailableBy, r)
	}
	sort.Strings(m.UsageUnavailableBy)
	for r := range costReasons {
		m.CostUnavailableBy = append(m.CostUnavailableBy, r)
	}
	sort.Strings(m.CostUnavailableBy)
	return m
}

// Metric names, in display order.
var MetricNames = []string{"recall", "false blockers", "escapes", "no verdict", "finding match", "wall time", "tokens", "cost"}

// Cell renders one metric of m. A ratio is "n/d"; a figure the adapter did not
// report names the reason rather than printing 0.
func (m Metrics) Cell(name string) string {
	if m.Rows == 0 && (name == "wall time" || name == "tokens" || name == "cost") {
		return "n/a"
	}
	switch name {
	case "recall":
		return ratio(m.Caught, m.Defect)
	case "false blockers":
		return ratio(m.FalseBlockers, m.Valid)
	case "escapes":
		return ratio(m.Escapes, m.Defect)
	case "no verdict":
		return fmt.Sprintf("%d", m.NoVerdict)
	case "finding match":
		if m.FindingScored == 0 {
			return FindingUnscored
		}
		s := ratio(m.FindingMatched, m.FindingScored)
		if m.FindingUnscored > 0 {
			s += fmt.Sprintf(" (+%d unscored)", m.FindingUnscored)
		}
		return s
	case "wall time":
		return (time.Duration(m.WallMs) * time.Millisecond).Round(time.Millisecond).String()
	case "tokens":
		if m.UsageRuns == 0 {
			return m.unavailable()
		}
		s := fmt.Sprintf("%d in / %d out", m.TokensIn, m.TokensOut)
		if m.UsageRuns < m.Rows {
			s += fmt.Sprintf(" (partial %d/%d runs; %s)", m.UsageRuns, m.Rows, m.unavailable())
		}
		return s
	case "cost":
		if m.CostRuns == 0 {
			return m.costUnavailable()
		}
		s := fmt.Sprintf("$%.4f", m.CostUSD)
		if m.CostRuns < m.Rows {
			s += fmt.Sprintf(" (partial %d/%d runs; %s)", m.CostRuns, m.Rows, m.costUnavailable())
		}
		return s
	}
	return ""
}

// costUnavailable names why cost is missing: the adapter's reason for tokens
// that came without a cost, and its reason for runs that reported no usage.
func (m Metrics) costUnavailable() string {
	if len(m.CostUnavailableBy) == 0 {
		return m.unavailable()
	}
	reasons := append([]string{}, m.CostUnavailableBy...)
	for _, r := range m.UsageUnavailableBy {
		reasons = append(reasons, r)
	}
	return strings.Join(reasons, "; ")
}

func (m Metrics) unavailable() string {
	if len(m.UsageUnavailableBy) == 0 {
		return "unavailable (the adapter reported no cost)"
	}
	return strings.Join(m.UsageUnavailableBy, "; ")
}

func ratio(n, d int) string {
	if d == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d (%.0f%%)", n, d, 100*float64(n)/float64(d))
}
