// Baseline agent seats (sty_6602bb44): the logical seats the shipped route
// references, embedded as data (substrate/config/agents.toml), not a Go table.
package config

import "sync"

// baselineAgentsFile is the embed-relative path of the baseline seat list.
const baselineAgentsFile = "substrate/config/agents.toml"

var (
	baselineAgentsOnce sync.Once
	baselineAgents     AgentsConfig
	baselineAgentsErr  error
)

// BaselineAgents returns the embedded baseline seats, decoded through the same
// loader as a repo agents file so it is held to the same validation.
//
// Include-by-name: a repo agents file names only the seats it changes. A seat
// it does not name stays the baseline's; a seat it names overrides only the
// fields it writes. The baseline carries seat identity only — the command,
// tools and model come from a machine profile (ResolveAgentsBaseline).
func BaselineAgents() (AgentsConfig, error) {
	baselineAgentsOnce.Do(func() {
		raw, err := substrateFS.ReadFile(baselineAgentsFile)
		if err != nil {
			baselineAgentsErr = err
			return
		}
		baselineAgents, baselineAgentsErr = loadAgentsBody(string(raw))
	})
	if baselineAgentsErr != nil {
		return AgentsConfig{}, baselineAgentsErr
	}
	out := baselineAgents
	out.Agents = make(map[string]AgentBinding, len(baselineAgents.Agents))
	for k, v := range baselineAgents.Agents {
		out.Agents[k] = v
	}
	return out, nil
}
