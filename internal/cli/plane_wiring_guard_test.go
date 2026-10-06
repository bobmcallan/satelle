package cli

import (
	"testing"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
)

// The wiring guard is built from the process of record (the main tree's
// configuration), never the invoking worktree's own (sty_f141c77f AC3).
func TestPlaneWiringGuardReadsTheMainTreeConfiguration(t *testing.T) {
	var worktreeOwn, mainTree config.Config
	worktreeOwn.Worktree.AbsentWiring = config.AbsentWiringFailOpen
	worktreeOwn.Harness = map[string]config.HarnessConfig{"claude": {GateWiring: []string{"own/wiring"}}}
	mainTree.Worktree.AbsentWiring = config.AbsentWiringRefuse
	mainTree.Harness = map[string]config.HarnessConfig{"claude": {GateWiring: []string{"main/wiring"}}}

	a := &app.App{Config: worktreeOwn, ProcessConfig: mainTree, ProcessRoot: t.TempDir(), RepoRoot: t.TempDir()}
	g := planeWiringGuard(a)
	if g.Policy != config.AbsentWiringRefuse {
		t.Errorf("policy = %q, want the main tree's refuse", g.Policy)
	}
	if got := g.GateWiring("claude"); len(got) != 1 || got[0] != "main/wiring" {
		t.Errorf("gate wiring = %v, want the main tree's", got)
	}
}
