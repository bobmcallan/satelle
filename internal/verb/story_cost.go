package verb

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// StoryCostRow is one dispatched/reviewed step's recorded cost — the per-gate
// tokens + wall-time captured on an agent_invocation ledger entry (sty_a699ad14).
type StoryCostRow struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Agent       string `json:"agent"`
	Skill       string `json:"skill,omitempty"`
	Model       string `json:"model,omitempty"`
	TokensIn    int    `json:"tokens_in"`
	TokensOut   int    `json:"tokens_out"`
	TokensTotal int    `json:"tokens_total"`
	DurationMs  int64  `json:"duration_ms"`
}

// stepCostData is a single step's self-reported actual tokens and/or its
// per-step estimate. It carries numbers and the step name ONLY — never env or
// secrets (sty_3b2e55f5). Read from two sources: the legacy KindStepCost ledger
// entry (retired writer, kept readable for history) and a KindTelemetryEvent row
// whose kind is "step-self-report" (the current writer, `satelle story log`,
// sty_b73c3236) — so the shape is single-sourced across both.
type stepCostData struct {
	Step          string `json:"step"`
	TokensTotal   int    `json:"tokens_total,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
	EstTokens     int    `json:"est_tokens,omitempty"`
	EstDurationMs int64  `json:"est_duration_ms,omitempty"`
}

// stepSelfReportKind is the telemetry event kind that expresses the retired
// step-cost verb's function via the generic `satelle story log` primitive.
const stepSelfReportKind = "step-self-report"

// StoryStepRow is one workflow STEP's cost report: the wall-time the story spent in
// that state (derived from the transition timestamps — this is how an IN-LOOP step,
// whose tokens a subprocess can't measure, still gets a recorded cost) plus, where
// `satelle story step-cost` recorded them, the step's self-reported actual tokens
// and its per-step estimate. This is the per-step est-vs-actual view (sty_3b2e55f5).
type StoryStepRow struct {
	Step          string `json:"step"`
	WallTimeMs    int64  `json:"wall_time_ms"`
	TokensTotal   int    `json:"tokens_total,omitempty"` // self-reported in-loop actual
	HasTokens     bool   `json:"has_tokens,omitempty"`   // false = unrecorded (a subprocess can't measure it), NOT free
	EstTokens     int    `json:"est_tokens,omitempty"`
	EstDurationMs int64  `json:"est_duration_ms,omitempty"`
}

// StoryCost is the per-story rollup: the dispatched/reviewed invocations (Rows, the
// precise sub-process cost) AND the per-step report (Steps, every state's wall-time
// plus any self-reported in-loop tokens / per-step estimate). Together they make the
// full cost of a driven story legible — dispatched and in-loop (sty_a699ad14,
// sty_3b2e55f5).
type StoryCost struct {
	StoryID         string         `json:"story_id"`
	Rows            []StoryCostRow `json:"rows"`
	Steps           []StoryStepRow `json:"steps,omitempty"`
	TotalTokens     int            `json:"total_tokens"`
	TotalDurationMs int64          `json:"total_duration_ms"`
	TotalWallMs     int64          `json:"total_wall_ms,omitempty"`
}

// mergeStepCost folds a self-reported actual + estimate onto r — the last
// report for a step wins (a re-record overrides), matching whichever writer
// produced it (the retired KindStepCost verb, or the current `story log
// --kind step-self-report`).
func mergeStepCost(r *StoryStepRow, d stepCostData) {
	if d.TokensTotal > 0 {
		r.TokensTotal, r.HasTokens = d.TokensTotal, true
	}
	if d.EstTokens > 0 {
		r.EstTokens = d.EstTokens
	}
	if d.EstDurationMs > 0 {
		r.EstDurationMs = d.EstDurationMs
	}
	if d.DurationMs > 0 {
		r.WallTimeMs = d.DurationMs // an explicit actual duration overrides the derived one
	}
}

// ComputeStoryCost reads the story's ledger and builds two complementary views:
//   - Rows: the dispatched/reviewer agent_invocation cost (precise tokens + agent
//     wall-time), exactly as before — entries with no usage contribute zero.
//   - Steps: a per-step report. Each state's WALL-TIME is derived from the deltas
//     between consecutive status_transition timestamps (so IN-LOOP steps, which
//     spawn no measurable subprocess, still get a duration), and its self-reported
//     actual tokens + per-step estimate are merged from any step_cost entry.
//
// Rows stay oldest-first, matching the ledger order; Steps follow first-occurrence
// order across the lifecycle.
func ComputeStoryCost(ctx context.Context, storyID string) (StoryCost, error) {
	ls, err := requireLedger()
	if err != nil {
		return StoryCost{}, err
	}
	// All kinds for the story, oldest-first — one pass covers invocations,
	// transitions (for wall-time), and step_cost (self-report + estimate).
	entries, err := ls.ListByStory(ctx, storyID, "")
	if err != nil {
		return StoryCost{}, err
	}

	sc := StoryCost{StoryID: storyID}
	wall := map[string]int64{}          // state -> summed occupancy ms (across re-entries)
	steps := map[string]*StoryStepRow{} // state -> report row
	var order []string                  // first-occurrence order of steps
	seen := map[string]bool{}
	addStep := func(name string) *StoryStepRow {
		if name == "" {
			return nil
		}
		if r, ok := steps[name]; ok {
			return r
		}
		r := &StoryStepRow{Step: name}
		steps[name] = r
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
		return r
	}

	var prevAt time.Time
	prevSet := false
	for _, e := range entries {
		switch e.Kind {
		case ledger.KindStoryCreated:
			prevAt, prevSet = e.CreatedAt, true
		case ledger.KindStatusTransition:
			var p struct {
				From string `json:"from"`
				To   string `json:"to"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			// The story occupied p.From over [prevAt, this transition]; attribute that
			// wall-time to the From step. The final terminal state (no later transition)
			// never accrues here — correct, it did no post-entry work.
			if prevSet && p.From != "" {
				wall[p.From] += e.CreatedAt.Sub(prevAt).Milliseconds()
				addStep(p.From)
			}
			prevAt, prevSet = e.CreatedAt, true
		case ledger.KindAgentInvocation:
			if len(e.Payload) == 0 {
				continue
			}
			var row StoryCostRow
			if err := json.Unmarshal(e.Payload, &row); err != nil {
				continue
			}
			sc.Rows = append(sc.Rows, row)
			sc.TotalTokens += row.TokensTotal
			sc.TotalDurationMs += row.DurationMs
		case ledger.KindStepCost:
			// Legacy writer (retired, sty_b73c3236) — kept readable for history.
			if len(e.Payload) == 0 {
				continue
			}
			var d stepCostData
			if err := json.Unmarshal(e.Payload, &d); err != nil || d.Step == "" {
				continue
			}
			mergeStepCost(addStep(d.Step), d)
		case ledger.KindTelemetryEvent:
			if len(e.Payload) == 0 {
				continue
			}
			var env telemetryEnvelope
			if err := json.Unmarshal(e.Payload, &env); err != nil || env.Kind != stepSelfReportKind {
				continue
			}
			raw, err := json.Marshal(env.Data)
			if err != nil {
				continue
			}
			var d stepCostData
			if err := json.Unmarshal(raw, &d); err != nil || d.Step == "" {
				continue
			}
			mergeStepCost(addStep(d.Step), d)
		}
	}

	for _, name := range order {
		r := steps[name]
		if r.WallTimeMs == 0 { // not explicitly set by step_cost -> use the derived occupancy
			r.WallTimeMs = wall[name]
		}
		sc.Steps = append(sc.Steps, *r)
		sc.TotalWallMs += r.WallTimeMs
	}
	return sc, nil
}
