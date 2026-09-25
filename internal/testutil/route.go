package testutil

import "strings"

// baselineGateSkills are the always-on gates the shipped step.toml declares. A
// repo step.toml overlays the shipped route by name (sty_a4603ea2), so a
// fixture that writes a step catalogue inherits them unless it says otherwise.
var baselineGateSkills = []string{"satelle-step-summary", "satelle-estimate-actual-review"}

// UngatedStep returns a fixture step.toml body with every shipped always-on gate
// the fixture does not declare itself scoped to a category no route uses, so a
// fixture written as a complete route stays as gated as it declares.
func UngatedStep(step string) string {
	var b strings.Builder
	b.WriteString(step)
	for _, skill := range baselineGateSkills {
		if strings.Contains(step, skill) {
			continue
		}
		b.WriteString("\n[[gate]]\nskill = \"" + skill + "\"\nfor = [\"unused\"]\n")
	}
	return b.String()
}
