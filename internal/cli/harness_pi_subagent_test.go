package cli

import (
	"strings"
	"testing"
)

// TestPiExposesNoSubagentHook pins the premise behind pi carrying no subagent
// qualifier on its driver-usage rows (sty_6deecf76): the pi extension registers no
// subagent hook and its tool matchers name no subagent spawner, so every pi
// session's spend is in its own record.
func TestPiExposesNoSubagentHook(t *testing.T) {
	for _, b := range piBindings() {
		for _, s := range []string{b.Event, b.Pi, b.Verb, b.Tools} {
			if strings.Contains(strings.ToLower(s), "subagent") {
				t.Errorf("pi binding %+v names a subagent", b)
			}
		}
	}
	if strings.Contains(strings.ToLower(piExtensionTemplate), "subagent") {
		t.Error("the pi extension template names a subagent")
	}
	hs := harnessHooks("pi")
	for _, m := range []string{hs.gateMatcher, hs.commitMatcher} {
		for _, spawner := range []string{"task", "agent", "spawn_subagent"} {
			if strings.Contains(strings.ToLower(m), spawner) {
				t.Errorf("pi tool matcher %q names the spawner %q", m, spawner)
			}
		}
	}
}
