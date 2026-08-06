//go:build integration

package tests

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/structure"
)

// TestSubstrateOnlyCheckScript is retained as a thin alias pointing at the
// generalised fence table (sty_6830e78e). Full cases live under
// fenceFixtures["satelle-substrate-only-check"] in substrate_check_fence_test.go.
//
// The allow-list shape pin (managed footprint) is a property the golden table
// cannot alone express as a corpus rule — it protects the shipped script's
// default allow expression, which is why this pin survives with a comment.
func TestSubstrateOnlyCheckScript(t *testing.T) {
	var body string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "skills" && d.Name == "satelle-substrate-only-check" {
			body = d.Body
			break
		}
	}
	if body == "" {
		t.Fatal("embedded satelle-substrate-only-check missing")
	}
	s := structure.CheckFence(body)
	if s == "" {
		t.Fatal("shipped satelle-substrate-only-check carries no ```check block")
	}
	// Surviving pin: the default allow-list covers the managed footprint. Stated
	// as CONTAINMENT, not an exact expression — the footprint grows when the
	// binary starts deploying somewhere new, and the seeded edit_exempt_paths
	// default must grow with it. internal/cli asserts the two agree (sty_926cfcdc);
	// this asserts the shipped expression names each one. Tables assert
	// accept/reject behaviour; this asserts the expression itself.
	for _, want := range []string{`\.satelle/`, `docs/`, `\.gitignore$`, `\.claude/`, `\.grok/`, `\.codex/`} {
		if !strings.Contains(s, want) {
			t.Fatalf("shipped allow-list missing managed footprint %q:\n%s", want, s)
		}
	}
	// Behaviour cases are in TestCheckFenceGoldenTables.
	if _, ok := fenceFixtures["satelle-substrate-only-check"]; !ok {
		t.Fatal("fenceFixtures missing satelle-substrate-only-check")
	}
}
