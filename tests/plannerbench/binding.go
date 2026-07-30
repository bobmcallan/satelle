//go:build plannerbench

package plannerbench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// shippedPlannerSection is the agents.toml section the project workflow's plan
// node allocates. The benchmark reads its grant from the repo's own file rather
// than restating one, so the study measures the tool policy the planner actually
// ships with (AC11). Nothing here hardcodes a grant string.
const shippedPlannerSection = "planner"

// shippedGrant is the resolved shipped planner tool grant plus the provenance a
// record needs to prove which file it came from.
type shippedGrant struct {
	Path   string `json:"path"`
	Grant  string `json:"grant"`
	SHA256 string `json:"sha256"`
}

// loadShippedGrant reads the repo's [planner] tools grant. The path is
// overridable so the hermetic suite can point at a fixture agents.toml; the
// default is this repo's live file, which is the point of the check.
func loadShippedGrant(path string) (shippedGrant, error) {
	if base := filepath.Base(path); base != config.AgentsConfigName {
		return shippedGrant{}, fmt.Errorf(
			"shipped grant path %s must be named %s (config.LoadAgents resolves it from its directory)",
			path, config.AgentsConfigName)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return shippedGrant{}, fmt.Errorf("read shipped agents.toml %s: %w", path, err)
	}
	agents, err := config.LoadAgents(filepath.Dir(path))
	if err != nil {
		return shippedGrant{}, fmt.Errorf("load shipped agents.toml %s: %w", path, err)
	}
	binding, ok := agents.NamedBinding(shippedPlannerSection)
	if !ok {
		return shippedGrant{}, fmt.Errorf("%s declares no [%s] section", path, shippedPlannerSection)
	}
	grant := strings.TrimSpace(binding.Tools)
	if grant == "" {
		return shippedGrant{}, fmt.Errorf("%s [%s] declares no tools grant", path, shippedPlannerSection)
	}
	return shippedGrant{Path: path, Grant: grant, SHA256: digest(string(raw))}, nil
}

// shippedAgentsPath resolves where to read the shipped grant from.
func shippedAgentsPath() string {
	if p := strings.TrimSpace(os.Getenv("SATELLE_PLANNER_AGENTS_TOML")); p != "" {
		return p
	}
	return filepath.Join("..", "..", ".satelle", "agents.toml")
}
