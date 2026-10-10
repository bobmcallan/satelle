package agentstep

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/worktree"
)

// WiringGuard is the gate-wiring policy a dispatch is held to (sty_f141c77f).
// Both fields come from the main tree's configuration (App.PlaneConfig), never
// from the worktree being dispatched into, so a worktree cannot exempt itself.
type WiringGuard struct {
	// GateWiring resolves the tree-relative paths a harness needs present
	// (config.Config.GateWiring).
	GateWiring func(harness string) []string
	// Policy is config.AbsentWiringRefuse or config.AbsentWiringFailOpen; empty
	// means refuse.
	Policy string
}

// WiringGuardFrom builds the guard from a configuration. The caller passes the
// main tree's (process-of-record) configuration, not the invoking worktree's.
func WiringGuardFrom(cfg config.Config) WiringGuard {
	return WiringGuard{GateWiring: cfg.GateWiring, Policy: cfg.AbsentWiringPolicy()}
}

// Missing returns what the tree at root lacks of the gate wiring declared for
// harness: each declared path that is absent, each hook wrapper a present wiring
// file calls that does not exist, or one entry saying the harness declares no
// wiring at all (label names the harness in that entry). Empty means the tree is
// wired. It is the check the dispatch guard runs, exported so a test can ask it
// of a real worktree.
func (w WiringGuard) Missing(root, harness, label string) []string {
	var declared []string
	if w.GateWiring != nil {
		declared = w.GateWiring(harness)
	}
	if len(declared) == 0 {
		return []string{"no gate wiring declared for harness " + label}
	}
	return missingWiring(root, declared)
}

// wiringMarker is the hook wrapper's file name. A wiring file that calls the
// wrapper is only gated while the wrapper it names exists, so the guard checks
// that too.
var wiringMarker = filepath.Base(config.HookWrapperRel)

// maxWiringScan bounds how much of one wiring file the guard reads.
const maxWiringScan = 1 << 20

// SetWiringGuard wires the guard. An engine without one dispatches unguarded —
// the CLI always wires it (cli.applyAgentGrants).
func (g *Engine) SetWiringGuard(w WiringGuard) { g.wiring = &w }

// wiringGuard runs before a named performer starts, in a tree it will edit. It
// does nothing outside a linked worktree (drift detection covers the main
// tree). When the harness's gate wiring is absent from that tree it refuses the
// dispatch, or under [worktree] absent_wiring = "fail-open" lets it run and
// says so — a warning and a ledger row — because an ungated run must be visible
// even when allowed.
func (g *Engine) wiringGuard(ctx context.Context, agent, command, itemID string) error {
	if g.wiring == nil || g.wiring.GateWiring == nil || !worktree.IsLinked(ctx, g.repoRoot) {
		return nil
	}
	harness := agentcli.HarnessOf(command)
	label := harness
	if harness == agentcli.HarnessUnknown {
		if f := strings.Fields(command); len(f) > 0 {
			label = fmt.Sprintf("%s (executable %q)", harness, filepath.Base(f[0]))
		}
	}
	missing := g.wiring.Missing(g.repoRoot, harness, label)
	if len(missing) == 0 {
		return nil
	}
	if g.wiring.Policy == config.AbsentWiringFailOpen {
		g.warnf("UNGATED: [worktree] absent_wiring = %q — harness %s: %s; this performer runs in %s without edit/commit gates",
			config.AbsentWiringFailOpen, label, strings.Join(missing, ", "), g.repoRoot)
		g.recordInvocation(ctx, itemID, map[string]any{
			"agent": agent, "phase": "ungated_dispatch", "harness": harness,
			"missing": missing, "policy": config.AbsentWiringFailOpen,
		})
		return nil
	}
	return fmt.Errorf("dispatch refused: worktree %s lacks gate wiring for harness %s: %s — run 'satelle story worktree <id> --existing', declare [harness.%s] gate_wiring, or set [worktree] absent_wiring = %q",
		g.repoRoot, label, strings.Join(missing, ", "), harness, config.AbsentWiringFailOpen)
}

// missingWiring returns every declared path absent under root, plus every hook
// wrapper a present wiring file calls that does not exist where the call points.
func missingWiring(root string, declared []string) []string {
	var missing []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			missing = append(missing, p)
		}
	}
	for _, rel := range declared {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err != nil {
			add(rel)
			continue
		}
		for _, ref := range wrapperRefs(abs) {
			target := ref
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, filepath.FromSlash(target))
			}
			if _, err := os.Stat(target); err != nil {
				add(ref)
			}
		}
	}
	return missing
}

// wrapperRefs scans one wiring file for the paths it gives the hook wrapper: a
// plain token scan over non-comment lines, not a per-harness parse. A token runs
// from the nearest preceding delimiter to the end of the wrapper's name.
func wrapperRefs(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf, _ := io.ReadAll(io.LimitReader(f, maxWiringScan))
	var refs []string
	for _, line := range strings.Split(string(buf), "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
			continue
		}
		for off := 0; ; {
			i := strings.Index(line[off:], wiringMarker)
			if i < 0 {
				break
			}
			end := off + i + len(wiringMarker)
			start := off + i
			for start > 0 && !strings.ContainsRune(" \t\"'`(=,", rune(line[start-1])) {
				start--
			}
			refs = append(refs, line[start:end])
			off = end
		}
	}
	return refs
}
