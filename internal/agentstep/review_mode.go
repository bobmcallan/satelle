package agentstep

import (
	"context"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Review mode (sty_93359e0f). A gate declares in the route whether its reviewer
// judges the whole artifact (full, the default) or stops at the first blocking
// finding (fail-fast). The binary carries the declaration to the seat in its
// payload and records what the verdict says it covered; the rubric that turns
// the mode into behaviour stays in the repo's skills.

// edgeFailFast returns the entry reviewers the active workflow declares
// fail-fast on the from→to edge. Empty on any resolution failure: a mode is a
// declaration the route makes, and an unresolvable route runs the gate full
// exactly as it does today.
func (g *Engine) edgeFailFast(ctx context.Context, item workitem.Item, from, to string) []string {
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		return nil
	}
	for _, tr := range spec.Transitions {
		if tr.From == from && tr.To == to {
			return tr.FailFast
		}
	}
	return nil
}

// failFastModeOf is the mode an edge-named reviewer runs in: fail-fast when the
// step lists the skill, else empty (full).
func failFastModeOf(failFast []string, skill string) string {
	if containsStr(failFast, skill) {
		return wfdot.ReviewModeFailFast
	}
	return ""
}

// withReviewMode wraps a payload decorator so the payload names the gate's mode.
// Full (empty) leaves the payload untouched, so its bytes are unchanged.
func withReviewMode(mode string, next func(*transitionPayload)) func(*transitionPayload) {
	if mode == "" {
		return next
	}
	return func(tp *transitionPayload) {
		tp.ReviewMode = mode
		if next != nil {
			next(tp)
		}
	}
}
