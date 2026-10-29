package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentstep"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/health"
)

// BindingWiring reports each isolated binding whose command names a gate-wiring
// path its harness declares (config.Config.GateWiring) that is absent under
// repoRoot — e.g. a pi binding loading `-e .pi/extensions/satelle.ts` in a repo
// that never ran `satelle agents install pi`, whose first dispatch would fail on
// the load (sty_d15f6713). A binding whose command names none of the declared
// paths is not judged: it does not load them. Absence is decided by
// agentstep.WiringGuard.Missing, so the wrapper a present wiring file calls is
// checked too. `satelle agent validate` and doctor both call this, so the two
// cannot disagree.
func BindingWiring(repoRoot string, cfg config.Config, grants []agentvalidate.Grant) health.Findings {
	if strings.TrimSpace(repoRoot) == "" {
		return nil
	}
	type gap struct{ harness, path string }
	var order []gap
	bindings := map[gap][]string{}
	for _, g := range grants {
		if g.Backend == "in-loop" || g.Backend == "invalid" {
			continue
		}
		h := agentcli.HarnessOf(g.Command)
		named := namedWiring(g.Command, cfg.GateWiring(h))
		if len(named) == 0 {
			continue
		}
		guard := agentstep.WiringGuard{GateWiring: func(string) []string { return named }}
		for _, p := range guard.Missing(repoRoot, h, h) {
			k := gap{h, p}
			if _, ok := bindings[k]; !ok {
				order = append(order, k)
			}
			bindings[k] = append(bindings[k], "["+g.Name+"]")
		}
	}
	var out health.Findings
	for _, k := range order {
		out = append(out, health.Error(health.IDAgentsWiringAbsent, "Harness wiring absent",
			fmt.Sprintf("%s harness %s loads %s, absent in %s", strings.Join(bindings[k], ", "), k.harness, k.path, repoRoot)).
			WithRemediation("run `satelle agents install "+k.harness+"`").
			About(k.path))
	}
	return out
}

// namedWiring is the subset of declared that command names as a whole word.
func namedWiring(command string, declared []string) []string {
	if len(declared) == 0 {
		return nil
	}
	words := map[string]bool{}
	for _, f := range strings.Fields(command) {
		words[filepath.Clean(f)] = true
	}
	var named []string
	for _, p := range declared {
		if words[filepath.Clean(filepath.FromSlash(p))] {
			named = append(named, p)
		}
	}
	return named
}
