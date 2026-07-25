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
	// Surviving pin: default allow-list includes the managed harness footprint.
	// Tables assert accept/reject behaviour; this asserts the expression itself.
	if !strings.Contains(s, `allow='\.satelle/|docs/|\.gitignore$|\.claude/|\.grok/'`) &&
		!strings.Contains(s, `allow='\.satelle/|docs/'`) {
		t.Fatalf("shipped allow-list missing managed footprint:\n%s", s)
	}
	// Behaviour cases are in TestCheckFenceGoldenTables.
	if _, ok := fenceFixtures["satelle-substrate-only-check"]; !ok {
		t.Fatal("fenceFixtures missing satelle-substrate-only-check")
	}
}
