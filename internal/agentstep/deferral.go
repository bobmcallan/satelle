package agentstep

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// DeferOwnerGrant is the owner of an objection whose only remedy lies outside
// the coder's authored tool grant. Every other owner is a reviewer skill named
// in the consult payload's later_gates.
const DeferOwnerGrant = "grant"

// ReworkRelayMarker is the `relay` value that identifies a rework relay's
// result row among a story's agent_invocation rows. Dispatch-attempt rows and
// the executor-step row also carry `outcome`, so `outcome` can never be the
// discriminator; only the relay writes this key.
const ReworkRelayMarker = "rework"

// DeferredItem is one objection the consult seat recorded as owned by something
// other than the coder: the grant (it cannot be cleared by the coder at all) or
// a later gate's skill (the evidence is that gate's to produce).
type DeferredItem struct {
	Owner     string `json:"owner"`
	Objection string `json:"objection"`
}

// LaterGate is one reviewer gate on a route step after the current one.
type LaterGate struct {
	Step  string `json:"step"`
	Skill string `json:"skill"`
}

// ReworkContext is what the consult seat is told about the performer it is
// reviewing: the coder binding, that binding's authored tool grant VERBATIM
// (satelle never interprets it), and every later gate that owns evidence the
// coder cannot produce. Enumeration, not verdict.
type ReworkContext struct {
	CoderBinding string      `json:"coder_binding"`
	CoderTools   string      `json:"coder_tools"`
	LaterGates   []LaterGate `json:"later_gates"`
}

// ReworkRelayRecord is the relay's result row — the single owner of its shape,
// written by `story rework` and read back by DeferralsFrom.
type ReworkRelayRecord struct {
	Relay         string         `json:"relay"`
	Converged     bool           `json:"converged"`
	Outcome       string         `json:"outcome"`
	Rounds        int            `json:"rounds"`
	LastObjection string         `json:"last_objection"`
	Coder         string         `json:"coder"`
	Consult       string         `json:"consult"`
	Budget        int            `json:"budget"`
	StoppedReason string         `json:"stopped_reason,omitempty"`
	Deferred      []DeferredItem `json:"deferred,omitempty"`
}

// DeferralsFrom enumerates the deferrals recorded by rework relays among a
// story's ledger rows, oldest first and de-duplicated. Only rows that decode to
// a ReworkRelayRecord with Relay == ReworkRelayMarker count.
func DeferralsFrom(entries []ledger.Entry) []DeferredItem {
	var out []DeferredItem
	seen := map[DeferredItem]bool{}
	for _, e := range entries {
		if e.Kind != ledger.KindAgentInvocation {
			continue
		}
		var rec ReworkRelayRecord
		if json.Unmarshal(e.Payload, &rec) != nil || rec.Relay != ReworkRelayMarker {
			continue
		}
		for _, d := range rec.Deferred {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// ConsultSeed is SessionSeed for the rework relay's consult seat: the same story
// payload plus the ReworkContext, so the consultant knows what the coder can run
// and which gates own the evidence it cannot.
func (g *Engine) ConsultSeed(ctx context.Context, item workitem.Item, binding string, rc ReworkContext) (string, error) {
	tp, err := g.ChatPayload(ctx, item, binding)
	if err != nil {
		return "", err
	}
	if rc.LaterGates == nil {
		rc.LaterGates = []LaterGate{}
	}
	tp.Rework = &rc
	b, err := json.Marshal(tp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SetDeferralsResolver wires the resolver that lists the deferrals a rework
// relay recorded for an item (every owner, oldest first). Enumeration, not
// verdict. Nil-safe: an unwired resolver injects nothing.
func (g *Engine) SetDeferralsResolver(fn func(ctx context.Context, itemID string) []DeferredItem) {
	g.deferrals = fn
}

// fillDeferred attaches the deferrals owned by skill to its gate payload and
// material. A gate that owns none is left byte-for-byte unchanged.
func (g *Engine) fillDeferred(ctx context.Context, itemID, skill string, tp *transitionPayload, mat *reviewerMaterial) {
	if g.deferrals == nil || itemID == "" {
		return
	}
	var own []DeferredItem
	for _, d := range g.deferrals(ctx, itemID) {
		if strings.TrimSpace(d.Owner) == skill {
			own = append(own, d)
		}
	}
	if len(own) == 0 {
		return
	}
	tp.Deferred = own
	if mat != nil {
		mat.Deferred = append([]DeferredItem(nil), own...)
	}
}

// mergeDeferred concatenates the deferrals of a bundle's gates, each keeping its
// owner label, without duplicates.
func mergeDeferred(preps []reviewerPrep) []DeferredItem {
	var out []DeferredItem
	seen := map[DeferredItem]bool{}
	for _, p := range preps {
		for _, d := range p.tp.Deferred {
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}
