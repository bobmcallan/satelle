package wfgovern

import (
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/wfdot"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// Route drift (sty_6e4f7fd8). A story's `workflow:` stamp records the LIFECYCLE;
// the LANE inside a derived route is re-derived from the story's category at
// every transition. So altering a category table — or adding one — re-lanes every
// in-flight story of that category, with no re-stamp and no notice. A story whose
// walked states are not states of the lane it derives NOW has drifted.
//
// This file ENUMERATES and REPORTS. It runs no reviewer, names no authored skill,
// and decides nothing about whether a given drift is tolerable — that verdict is
// configuration (satelle-route-drift-check), named by a repo on the step it wants
// guarded. The one refusal the binary does make is structural, not a verdict: a
// status that is not a state of the route has no legal edge at all.

// RouteDrift is one story's walked lane measured against the lane its category
// derives now. Enumeration only — every field is a fact, none is a verdict.
type RouteDrift struct {
	Item     string `json:"item"`
	Category string `json:"category"`
	Status   string `json:"status"`
	// Walked is the states the route document records this story as having
	// reached, in order.
	Walked []string `json:"walked"`
	// Derived is the states of the lane the category derives now.
	Derived []string `json:"derived"`
	// OffRoute is the walked states that are not states of Derived — the drift
	// itself.
	OffRoute []string `json:"off_route"`
	// StatusOnRoute reports whether the story's CURRENT status is still a state
	// of the derived lane. False means no legal edge exists from where it sits.
	StatusOnRoute bool `json:"status_on_route"`
}

// DetectRouteDrift measures walked against derived. ok=false — the fast path —
// when the story has walked nothing yet or every walked state is on the lane,
// which is every ordinary transition in a repo whose tables did not change.
func DetectRouteDrift(item workitem.Item, spec wfdot.Spec, walked []string) (RouteDrift, bool) {
	states := map[string]bool{}
	var derived []string
	for _, st := range spec.States {
		if st.Name == "" || len(st.On) > 0 {
			continue // an always-on gate node occupies no stage
		}
		if !states[st.Name] {
			states[st.Name] = true
			derived = append(derived, st.Name)
		}
	}
	if len(derived) == 0 {
		return RouteDrift{}, false // nothing derived to measure against
	}
	var off []string
	for _, w := range walked {
		if !states[w] {
			off = append(off, w)
		}
	}
	onRoute := states[item.Status]
	if len(off) == 0 && onRoute {
		return RouteDrift{}, false
	}
	sort.Strings(off)
	return RouteDrift{
		Item:          item.ID,
		Category:      item.Category,
		Status:        item.Status,
		Walked:        walked,
		Derived:       derived,
		OffRoute:      off,
		StatusOnRoute: onRoute,
	}, true
}

// DriftRemedy is the mutability-aware fix. Category is a frozen definition field
// once a story leaves its entry state, so the two spellings are not stylistic:
// suggesting --category to a story past the entry state sends the operator into
// a refusal.
func DriftRemedy(d RouteDrift, entryState string) string {
	if d.Status == entryState {
		return "reclassify now — `satelle story set " + d.Item + " --category <category>` — category is still mutable in " + entryState +
			", and this is the last step where the cheap fix exists"
	}
	return "category is frozen once a story leaves " + entryState +
		", so this one cannot be reclassified: cancel it with a recorded reason (`--add-tags cancel-reason:superseded`) and re-raise the work carrying `supersedes:" + d.Item + "`"
}

// DriftWhy states the fact both lanes make plain, for the refusal's Why.
func DriftWhy(d RouteDrift) string {
	return d.Status + " is not a state of the route category " + d.Category + " derives now (" +
		strings.Join(d.Derived, " → ") + "); this story walked " + strings.Join(d.Walked, " → ") +
		". The lane changed under it: a category table selects the lane at every transition, so altering one re-lanes work already in flight"
}
