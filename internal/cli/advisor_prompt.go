package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/verb"
)

// workflowAdvisorSkill is the rubric init and a passing validate prompt the
// orchestrator to run. Those seams have no route step to declare an advisor; the
// epic readiness seam prints whatever its readiness step declares instead.
const workflowAdvisorSkill = "satelle-workflow-advisor"

// advisorPromptFor is the one line that prompts the orchestrator to run skill.
// satelle prints it and runs nothing. The wording names no subject: the epic
// readiness seam prints whichever advisor its step declares.
func advisorPromptFor(skill string) string {
	return fmt.Sprintf("advise: consult the %s skill (satelle doc get skills %s) — advisory, satelle runs and applies nothing", skill, skill)
}

// advisorPrompt is the prompt init and a passing validate print.
func advisorPrompt() string { return advisorPromptFor(workflowAdvisorSkill) }

// emitAdvice prints one prompt per distinct skill unless --json was asked for.
// Each line is also recorded as a verdict line, so a detached gate run hands it
// to the driving session with the rest of its result.
func emitAdvice(cmd *cobra.Command, out io.Writer, skills []string) {
	if v, _ := cmd.Flags().GetBool("json"); v {
		return
	}
	var seen []string
	for _, s := range skills {
		if s == "" || slices.Contains(seen, s) {
			continue
		}
		seen = append(seen, s)
		line := advisorPromptFor(s)
		fmt.Fprintln(out, line)
		verb.RecordVerdict(line)
	}
}

// readyAdvice collects the advice on story ready results, nested children
// included: a container readied as another's child carries its own.
func readyAdvice(results []verb.ReadyResult) []string {
	var out []string
	for _, r := range results {
		out = append(out, r.Advise...)
		out = append(out, readyAdvice(r.Children)...)
	}
	return out
}

// setAdvice reads the advice a story-set response carries, if any.
func setAdvice(resp json.RawMessage) []string {
	var r struct {
		Advise []string `json:"advise"`
	}
	if len(resp) == 0 || json.Unmarshal(resp, &r) != nil {
		return nil
	}
	return r.Advise
}
