package cli

import (
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/help"
)

// The three help surfaces that describe a container's child schedule all say
// parallel is the default and sequential = true is the override.
func TestScheduleHelpNamesTheDefaultAndTheOverride(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	surfaces := map[string]string{}
	for _, name := range []string{"epic-wave", "workflow-convert"} {
		topic, ok := help.Get(name)
		if !ok {
			t.Fatalf("help topic %q not present", name)
		}
		surfaces["help "+name] = flat(topic.Body)
	}
	surfaces["story wave"] = flat(storyWaveCommand().Long)

	for name, body := range surfaces {
		lower := strings.ToLower(body)
		if !strings.Contains(lower, "parallel by default") {
			t.Errorf("%s must say parallel is the default", name)
		}
		if !strings.Contains(body, "sequential = true") {
			t.Errorf("%s must name sequential = true as the override", name)
		}
		if strings.Contains(lower, "no schedule is declared") || strings.Contains(lower, `the "no schedule" refusal`) {
			t.Errorf("%s still describes the retired no-schedule refusal", name)
		}
	}
	for _, name := range []string{"help epic-wave", "help workflow-convert"} {
		if !strings.Contains(surfaces[name], "deprecated") {
			t.Errorf("%s must say the schedule key is deprecated", name)
		}
	}
}
