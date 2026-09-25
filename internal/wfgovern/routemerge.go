package wfgovern

import (
	"bytes"
	"strings"

	"github.com/BurntSushi/toml"
)

// gateKey is the array-of-tables key a step catalogue carries its always-on
// gates under. A gate has no name, so the overlay identifies one by its skill.
const gateKey = "gate"

// EmbeddedRoute returns the binary's shipped body for a route-source half, or
// "" when there is none. It is injected rather than imported: config's own tests
// reach this package through structure, so importing config here is a cycle.
// store registers it, beside where it seeds the doc index defaults.
var EmbeddedRoute func(name string) string

// embeddedRouteBody reads the shipped baseline. The doc list cannot supply it:
// an authored file of the same name shadows the embedded doc there.
func embeddedRouteBody(name string) string {
	if EmbeddedRoute == nil {
		return ""
	}
	return EmbeddedRoute(name)
}

// mergeRouteHalf overlays a repo's done.toml or step.toml onto the shipped one
// BY NAME. A top-level table in the repo body (a category in done.toml, a step
// in step.toml, or meta) replaces the baseline table of that name whole; names
// the repo does not mention stay on the baseline. [[gate]] has no name: when
// the repo declares any, that list replaces the baseline list; when it
// declares none, the baseline gates stay. An empty repo body returns the
// baseline bytes unchanged.
//
// It never fails: when either side does not decode, the repo body is returned
// as written so the parse-error paths keep pointing at the repo's own file
// rather than at a synthesised one.
func mergeRouteHalf(baseline, repo string) string {
	if strings.TrimSpace(repo) == "" {
		return baseline
	}
	if strings.TrimSpace(baseline) == "" {
		return repo
	}
	var base, over map[string]any
	if _, err := toml.Decode(baseline, &base); err != nil {
		return repo
	}
	if _, err := toml.Decode(repo, &over); err != nil {
		return repo
	}
	for k, v := range over {
		if k != gateKey {
			base[k] = v
		}
	}
	// A repo that declares any gate owns the list. Gates have no name, so a
	// partial merge would keep baseline gates (the estimate fence, the step
	// summary) on a fixture or a repo that brought its own. No gate key at all
	// leaves the baseline list.
	if g, ok := over[gateKey]; ok {
		base[gateKey] = gateList(g)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(base); err != nil {
		return repo
	}
	return buf.String()
}

func gateList(v any) []map[string]any {
	switch t := v.(type) {
	case []map[string]any:
		return t
	case []any:
		var out []map[string]any
		for _, e := range t {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}
