package doctor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
	"github.com/bobmcallan/satelle/internal/health"
	"github.com/bobmcallan/satelle/internal/wfgovern"
)

// CheckCommandRuleAllowNames reports every [[gate.command_rule]] allow entry
// that names no state of any route in workflows (sty_a74551d6). Config load
// cannot judge this: it holds no workflow knowledge, and the hook must never
// load workflows. So a stale name is a finding here, for `satelle validate` and
// `satelle doctor`, and never makes the hook fail.
//
// workflows is the governing set (authored files plus the shipped defaults they
// do not shadow). When no route can be derived from it nothing is judged: the
// route's own defect is reported by its own check.
func CheckCommandRuleAllowNames(cfg config.Config, workflows []docindex.Doc) health.Findings {
	if len(cfg.Gate.CommandRules) == 0 {
		return nil
	}
	known := routeStateNames(workflows)
	if len(known) == 0 {
		return nil
	}
	var out health.Findings
	for i, r := range cfg.Gate.CommandRules {
		for _, a := range r.Allow {
			name := strings.ToLower(strings.TrimSpace(a))
			if name == "" || known[name] {
				continue
			}
			out = append(out, health.Error(health.IDCommandRuleAllow, "Command rule allows an unknown state",
				fmt.Sprintf("[[gate.command_rule]] rule %d field allow: %q is not a state of any workflow route", i, a)).
				WithRemediation("name a status or step the route declares ("+strings.Join(stateNameList(known), ", ")+"), or remove the entry").
				About(config.DefaultDataDir+"/"+config.ConfigName))
		}
	}
	return out
}

// routeStateNames is every state name, lowercased, across the routes of every
// category the done.toml declares.
func routeStateNames(workflows []docindex.Doc) map[string]bool {
	rs := wfgovern.RouteSourceOf(workflows)
	if rs.Err() != nil || !rs.Present() {
		return nil
	}
	known := map[string]bool{}
	for _, category := range wfgovern.RouteCategories(rs.Done) {
		d, err := wfgovern.RouteSpecFor(rs, category, nil)
		if err != nil {
			continue
		}
		for _, st := range d.Spec.States {
			known[strings.ToLower(st.Name)] = true
		}
	}
	return known
}

func stateNameList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
