package help

import (
	"strings"
	"testing"
)

// TestEpicWaveReadySentenceAgreesWithStoryReady (sty_87403c0e): the sentence
// describing `satelle story ready <epic>` states the same split on
// ready_children as the command's own help and the route.
func TestEpicWaveReadySentenceAgreesWithStoryReady(t *testing.T) {
	wave, ok := Get("epic-wave")
	if !ok {
		t.Fatal("epic-wave topic not found")
	}
	flat := strings.Join(strings.Fields(wave.Body), " ")
	want := "which judges the epic and, unless its readiness step declares `ready_children = false`, also runs each backlog child's readiness round (no round changes a status)"
	if !strings.Contains(flat, want) {
		t.Errorf("epic-wave topic missing %q", want)
	}
	if strings.Contains(flat, "readies the epic's children as part of its round") {
		t.Error("epic-wave still says an epic round readies its children unconditionally")
	}
}
