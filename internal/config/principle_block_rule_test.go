package config

import (
	"regexp"
	"strings"
	"testing"
)

// blockRuleLead opens the session-resident rule that a block is never worked
// around (sty_902140d6).
const blockRuleLead = "**A block is never worked around"

// sentenceEnd matches a sentence terminator: a full stop, question or
// exclamation mark followed by whitespace or the end of the paragraph.
var sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)

// TestAgentGoalsForbidsWorkingAroundABlock pins the rule in the embedded
// satelle-agent-goals principle: in at most three sentences, a block of any
// kind is never worked around, by the agent or through a command handed to the
// user; the work is raised as a story in the owning repo and fitting category;
// and a missing category or workflow is proposed and created only on approval.
func TestAgentGoalsForbidsWorkingAroundABlock(t *testing.T) {
	var body string
	for _, d := range EmbeddedDefaults() {
		if d.Kind == "principles" && d.Name == "satelle-agent-goals" {
			body = d.Body
		}
	}
	if body == "" {
		t.Fatal("embedded principle satelle-agent-goals not found")
	}

	fm, _, ok := strings.Cut(strings.TrimPrefix(body, "---\n"), "\n---\n")
	if !ok || !strings.Contains(fm, "principles:session") {
		t.Fatalf("satelle-agent-goals must stay session-resident (principles:session tag):\n%s", fm)
	}

	i := strings.Index(body, blockRuleLead)
	if i < 0 {
		t.Fatalf("satelle-agent-goals has no paragraph opening %q", blockRuleLead)
	}
	para, _, _ := strings.Cut(body[i:], "\n\n")
	para = strings.Join(strings.Fields(para), " ")

	if n := len(sentenceEnd.FindAllString(para, -1)); n == 0 || n > 3 {
		t.Errorf("block rule must be at most three sentences, got %d: %s", n, para)
	}
	for _, want := range []string{
		"gate",
		"hook deny",
		"edit gate",
		"cross-repo fence",
		"harness refusal",
		"hand the user a command",
		"story in the repo",
		"category whose route fits",
		"propose one",
		"approval",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("block rule missing %q: %s", want, para)
		}
	}
}
