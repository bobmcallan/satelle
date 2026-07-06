package verb

import (
	"context"
	"encoding/json"

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

// StoryCost is the per-story rollup of every gate/dispatch invocation's cost, so
// an operator can SEE which reviewer/step spent what — the observability that
// makes reviewer cost/benefit legible (sty_a699ad14).
type StoryCost struct {
	StoryID         string         `json:"story_id"`
	Rows            []StoryCostRow `json:"rows"`
	TotalTokens     int            `json:"total_tokens"`
	TotalDurationMs int64          `json:"total_duration_ms"`
}

// ComputeStoryCost reads the story's agent_invocation ledger entries and rolls up
// their recorded token/wall-time cost. Entries written before cost capture (or by
// a plain-text harness that emits no usage) contribute zero — expected and
// harmless. Rows are oldest-first, matching the ledger order.
func ComputeStoryCost(ctx context.Context, storyID string) (StoryCost, error) {
	ls, err := requireLedger()
	if err != nil {
		return StoryCost{}, err
	}
	entries, err := ls.ListByStory(ctx, storyID, ledger.KindAgentInvocation)
	if err != nil {
		return StoryCost{}, err
	}
	sc := StoryCost{StoryID: storyID}
	for _, e := range entries {
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
	}
	return sc, nil
}
