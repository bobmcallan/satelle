package agentstep

import (
	"context"

	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/wfgovern"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// routeDriftFor resolves the story's current lane and measures it against the
// walked lane. The walked half comes from verb.RouteWalked, which owns the
// outcome-heading format it parses.
func (g *Engine) routeDriftFor(ctx context.Context, item workitem.Item) (wfgovern.RouteDrift, bool) {
	spec, _, err := g.activeSpec(ctx, item)
	if err != nil {
		return wfgovern.RouteDrift{}, false // ungoverned or unparseable: not this guard's refusal
	}
	walked := verb.RouteWalked(item)
	if len(walked) == 0 {
		return wfgovern.RouteDrift{}, false
	}
	return wfgovern.DetectRouteDrift(item, spec, walked)
}
