package verb

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bobmcallan/satelle/internal/ledger"
)

// StoryCostRow is one dispatched/reviewed step's recorded cost — the per-gate
// tokens + wall-time captured on an agent_invocation ledger entry (sty_a699ad14).
// UsageAvailable is true only when the transport reported usage (or, for legacy
// rows without the field, when tokens_total > 0). False means unreported —
// never a measured zero (sty_56aae77a).
type StoryCostRow struct {
	From           string `json:"from"`
	To             string `json:"to"`
	Agent          string `json:"agent"`
	Skill          string `json:"skill,omitempty"`
	Model          string `json:"model,omitempty"`
	TokensIn       int    `json:"tokens_in"`
	TokensOut      int    `json:"tokens_out"`
	TokensTotal    int    `json:"tokens_total"`
	DurationMs     int64  `json:"duration_ms"`
	UsageAvailable bool   `json:"usage_available"`
}

// EventTelemetry is the verb-package façade over ledger.EventTelemetry so
// story-cost and the web timeline share one reader (sty_43d228e4). Ownership of
// the extraction lives in ledger so the push-fed web package need not import verb.
func EventTelemetry(e ledger.Entry) ledger.Telemetry {
	return ledger.EventTelemetry(e)
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
// sty_3b2e55f5, sty_56aae77a).
//
// TotalTokens sums measured rows only. UnmeasuredRows counts invocations whose
// provider reported no usage — they render as unknown and never feed the total.
type StoryCost struct {
	StoryID         string         `json:"story_id"`
	Rows            []StoryCostRow `json:"rows"`
	Steps           []StoryStepRow `json:"steps,omitempty"`
	TotalTokens     int            `json:"total_tokens"`
	TotalDurationMs int64          `json:"total_duration_ms"`
	TotalWallMs     int64          `json:"total_wall_ms,omitempty"`
	MeasuredRows    int            `json:"measured_rows,omitempty"`
	UnmeasuredRows  int            `json:"unmeasured_rows,omitempty"`
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
//     wall-time). Token totals and usage availability come solely from
//     ledger.EventTelemetry (sty_56aae77a) — this function does not re-infer.
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
			// Edge identity (from/to/skill) from the payload; token totals and
			// usage availability from the sole ledger reader (sty_56aae77a).
			var meta struct {
				From  string `json:"from"`
				To    string `json:"to"`
				Agent string `json:"agent"`
				Skill string `json:"skill"`
				Model string `json:"model"`
			}
			if err := json.Unmarshal(e.Payload, &meta); err != nil {
				continue
			}
			tel := ledger.EventTelemetry(e)
			row := StoryCostRow{
				From:           meta.From,
				To:             meta.To,
				Agent:          meta.Agent,
				Skill:          meta.Skill,
				Model:          meta.Model,
				TokensIn:       tel.TokensIn,
				TokensOut:      tel.TokensOut,
				TokensTotal:    tel.TokensTotal,
				DurationMs:     tel.DurationMs,
				UsageAvailable: tel.UsageAvailable,
			}
			// Prefer telemetry agent/model when meta left them empty (defensive).
			if row.Agent == "" {
				row.Agent = tel.Agent
			}
			if row.Model == "" {
				row.Model = tel.Model
			}
			sc.Rows = append(sc.Rows, row)
			sc.TotalDurationMs += row.DurationMs
			if row.UsageAvailable {
				sc.TotalTokens += row.TokensTotal
				sc.MeasuredRows++
			} else {
				sc.UnmeasuredRows++
			}
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

// HasStepSelfReport reports whether the story ledger already carries a
// step-self-report for step (used by the step-edge nudge, sty_56aae77a AC3).
func HasStepSelfReport(ctx context.Context, storyID, step string) bool {
	if step == "" {
		return false
	}
	sc, err := ComputeStoryCost(ctx, storyID)
	if err != nil {
		return false
	}
	for _, s := range sc.Steps {
		if s.Step == step && s.HasTokens {
			return true
		}
	}
	// HasTokens only true when tokens_total > 0; a report with only wall-time
	// still counts — scan ledger directly for the kind.
	ls, err := requireLedger()
	if err != nil {
		return false
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindTelemetryEvent)
	if err != nil {
		return false
	}
	for _, e := range entries {
		var env telemetryEnvelope
		if json.Unmarshal(e.Payload, &env) != nil || env.Kind != stepSelfReportKind {
			continue
		}
		if s, _ := env.Data["step"].(string); s == step {
			return true
		}
	}
	return false
}
