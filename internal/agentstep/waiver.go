package agentstep

import (
	"context"
	"strings"

	"github.com/bobmcallan/satelle/internal/verb"
)

// WaiverItem is one developer waiver — the named check of one gate on one
// story — as a gate's payload carries it (sty_bef97244). Gate is the skill the
// waiver names; a gate is only ever handed the waivers whose Gate is its own.
type WaiverItem struct {
	ID        string `json:"id"`
	Gate      string `json:"gate"`
	Check     string `json:"check"`
	Reason    string `json:"reason"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at,omitempty"`
}

// SetWaiversResolver wires the resolver that lists the developer waivers
// recorded for an item (every gate, oldest first). Enumeration, not verdict.
// Nil-safe: an unwired resolver injects nothing.
func (g *Engine) SetWaiversResolver(fn func(ctx context.Context, itemID string) []WaiverItem) {
	g.waivers = fn
}

// fillWaivers attaches the waivers whose gate is skill to its gate payload and
// material. A gate with none is left byte-for-byte unchanged.
func (g *Engine) fillWaivers(ctx context.Context, itemID, skill string, tp *transitionPayload, mat *reviewerMaterial) {
	if g.waivers == nil || itemID == "" {
		return
	}
	var own []WaiverItem
	for _, w := range g.waivers(ctx, itemID) {
		if strings.TrimSpace(w.Gate) == skill {
			own = append(own, w)
		}
	}
	if len(own) == 0 {
		return
	}
	tp.Waivers = own
	if mat != nil {
		mat.Waivers = append([]WaiverItem(nil), own...)
	}
}

// mergeWaivers concatenates the waivers of a bundle's gates, each keeping its
// gate label, without duplicates.
func mergeWaivers(preps []reviewerPrep) []WaiverItem {
	var out []WaiverItem
	seen := map[WaiverItem]bool{}
	for _, p := range preps {
		for _, w := range p.tp.Waivers {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	return out
}

// keepWaived trims dec.Waived to the checks this gate's payload actually carries
// a waiver for, so a reviewer cannot record a waiver that was never granted. It
// filters and decides nothing: whether the waiver was honoured is the rubric's.
func (p reviewerPrep) keepWaived(dec *verb.GateDecision) {
	if dec == nil || len(dec.Waived) == 0 {
		return
	}
	granted := map[string]bool{}
	for _, w := range p.tp.Waivers {
		granted[strings.TrimSpace(w.Check)] = true
	}
	var kept []string
	seen := map[string]bool{}
	for _, c := range dec.Waived {
		c = strings.TrimSpace(c)
		if granted[c] && !seen[c] {
			seen[c] = true
			kept = append(kept, c)
		}
	}
	dec.Waived = kept
}
