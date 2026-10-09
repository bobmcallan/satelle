package config

import (
	"strings"
	"testing"
)

func principleTagsLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "tags:") {
			return line
		}
	}
	return ""
}

// sty_29ce243b AC1/AC2: the session-resident goals principle names the driving
// session as the orchestrator, what starts the drive, and what a named performer
// hands back. The pins are the content, not its wording elsewhere.
func TestGoalsPrincipleNamesTheDrivingSessionAsOrchestrator(t *testing.T) {
	body := embeddedPrinciples()["satelle-agent-goals"]
	if !strings.Contains(principleTagsLine(body), "principles:session") {
		t.Fatalf("satelle-agent-goals must stay session-resident: %q", principleTagsLine(body))
	}
	flat := strings.Join(strings.Fields(body), " ")
	for _, want := range []string{
		"same agent",
		"complete sty_<id>",
		"drive epic <id> to done",
		"terminal state",
		"does not change status",
		"runs the next `satelle story set`",
		"fences source edits",
		"The drive stays in this session",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("satelle-agent-goals must contain %q", want)
		}
	}
}

// sty_29ce243b AC3: the on-demand agent model states the same identity and never
// gives the status request to the executor alone.
func TestAgentModelPrincipleHasOneDriver(t *testing.T) {
	body := embeddedPrinciples()["satelle-agent-model"]
	if strings.Contains(principleTagsLine(body), "principles:session") {
		t.Fatalf("satelle-agent-model must stay on demand: %q", principleTagsLine(body))
	}
	flat := strings.Join(strings.Fields(body), " ")
	if !strings.Contains(flat, "driving session is the **orchestrator**") {
		t.Error("satelle-agent-model must say the driving session is the orchestrator")
	}
	if strings.Contains(flat, "The driving session *is* the executor") {
		t.Error("satelle-agent-model must not describe the executor and orchestrator as two drivers")
	}
	// Every sentence that gives the status request names the driving session or
	// the orchestrator as the requester.
	const phrase = "requests the next status"
	for _, sentence := range strings.SplitAfter(flat, ". ") {
		if !strings.Contains(sentence, phrase) {
			continue
		}
		if !strings.Contains(sentence, "driving session") && !strings.Contains(sentence, "orchestrator") {
			t.Errorf("%q must be said of the driving session / orchestrator: %q", phrase, sentence)
		}
	}
	if !strings.Contains(flat, phrase) {
		t.Errorf("satelle-agent-model must say who %s", phrase)
	}
}
