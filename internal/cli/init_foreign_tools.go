package cli

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
)

// A machine layer (agents.local.toml, agents.workspace.toml) that restates a
// seat's tools in another adapter's names than the seat resolves to leaves the
// seat with no context channel: node.alloc fails, and so does init. `satelle
// init` repairs the line on raw bytes before anything validates (sty_f7f80c48):
// it drops the line when the profile already grants the same tools, restates it
// in the resolved adapter's names otherwise, and leaves — and reports — a tool
// that has no counterpart there. Doctor's warning (sty_259572f6) and this repair
// read one walk, agentvalidate.ForeignLayerSeats.

// seatToolRepair is what init makes of one foreign seat's tools line.
type seatToolRepair struct {
	Rel      string // the layer file, data-dir-relative
	Seat     string // the seat, which is also its table in the file
	Adapter  string // the adapter the seat resolves to
	Old      string // the tools value as written
	New      string // the value restated in Adapter's names
	Drop     bool   // the profile grants the same tools, so the line goes
	Unmapped []string
}

func (r seatToolRepair) file() string { return config.DefaultDataDir + "/" + r.Rel }

// rewrites reports whether the repair changes the file.
func (r seatToolRepair) rewrites() bool { return r.Drop || r.New != r.Old }

func (r seatToolRepair) describe() string {
	if r.Drop {
		return fmt.Sprintf("[%s] tools %s → dropped: the profile grants the same tools", r.Seat, r.Old)
	}
	return fmt.Sprintf("[%s] tools %s → %s, in the %s adapter's names", r.Seat, r.Old, r.New, r.Adapter)
}

func (r seatToolRepair) unmappedDetail(name string) string {
	return fmt.Sprintf("[%s] tool %s has no counterpart in the %s adapter's tool names — left as written, restate it by hand", r.Seat, name, r.Adapter)
}

// planForeignSeatToolRepairs reads the repo's agents layers and returns a repair
// for each seat ForeignLayerSeats names. It resolves without validating, so a
// broken seat cannot stop it; a layer or catalog it cannot resolve plans
// nothing, and init goes on as it would have. A seat whose tools are not a
// plain `tools = …` line in its own table is not planned (doctor still names it).
func planForeignSeatToolRepairs(dataDir string) []seatToolRepair {
	if !machineLayerNamesTools(dataDir) {
		return nil
	}
	layers, _ := config.LoadAgentLayers(dataDir)
	global, err := config.LoadGlobalAgents()
	if err != nil {
		return nil
	}
	baseline, err := config.BaselineAgents()
	if err != nil {
		return nil
	}
	eff, _, err := config.ResolveAgentsBaseline(baseline, layers, global)
	if err != nil {
		return nil
	}
	var out []seatToolRepair
	for _, s := range agentvalidate.ForeignLayerSeats(layers, eff) {
		raw, rerr := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(s.Rel)))
		if rerr != nil || !config.HasKey(string(raw), s.Seat, "tools") {
			continue
		}
		names, unmapped := agentcli.TranslateTools(s.Owner, s.Adapter, s.Tools)
		r := seatToolRepair{Rel: s.Rel, Seat: s.Seat, Adapter: s.Adapter, Old: s.Tools, New: strings.Join(names, ","), Unmapped: unmapped}
		// A line still granting a tool with no counterpart is never dropped: the
		// profile cannot be shown to grant it.
		if len(unmapped) == 0 {
			bare, _, berr := config.ResolveAgentsBaseline(baseline, withoutLayerTools(layers, s.Rel, s.Seat), global)
			if berr == nil && agentcli.SameTools(agentvalidate.SeatBinding(bare, s.Seat).Tools, r.New) {
				r.Drop = true
			}
		}
		out = append(out, r)
	}
	return out
}

// machineLayerNamesTools is the cheap first look, on raw bytes: with no tools
// key in either machine layer there is nothing to repair, and nothing — not the
// project tier, not the catalog — is loaded. It runs on every session start's
// drift check.
func machineLayerNamesTools(dataDir string) bool {
	for _, rel := range []string{config.LocalAgentsRel, config.WorkspaceAgentsRel} {
		if b, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel))); err == nil && strings.Contains(string(b), "tools") {
			return true
		}
	}
	return false
}

// withoutLayerTools is layers with seat's tools blanked in the layer file rel:
// what the seat would resolve to were the line dropped.
func withoutLayerTools(layers config.AgentLayers, rel, seat string) config.AgentLayers {
	blank := func(ac config.AgentsConfig) config.AgentsConfig {
		switch seat {
		case "executor":
			ac.Executor.Tools = ""
		case "reviewer":
			ac.Reviewer.Tools = ""
		default:
			ac.Agents = maps.Clone(ac.Agents)
			b := ac.Agents[seat]
			b.Tools = ""
			ac.Agents[seat] = b
		}
		return ac
	}
	switch rel {
	case config.LocalAgentsRel:
		layers.Local = blank(layers.Local)
	case config.WorkspaceAgentsRel:
		layers.Workspace = blank(layers.Workspace)
	}
	return layers
}

// repairForeignSeatTools applies planForeignSeatToolRepairs to the layer files
// on raw bytes — every other line, comments included, stays as written — and
// reports each rewrite and each tool it left. It writes a file only when its
// bytes changed.
func repairForeignSeatTools(out io.Writer, repoRoot, dataDir string) error {
	repairs := planForeignSeatToolRepairs(dataDir)
	edited := map[string]string{}
	original := map[string]string{}
	var order, lines []string
	for _, r := range repairs {
		for _, name := range r.Unmapped {
			lines = append(lines, fmt.Sprintf("! %s %s %s", repoRoot, r.file(), r.unmappedDetail(name)))
		}
		if !r.rewrites() {
			continue
		}
		path := filepath.Join(dataDir, filepath.FromSlash(r.Rel))
		content, seen := edited[path]
		if !seen {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content = string(raw)
			original[path] = content
			order = append(order, path)
		}
		if r.Drop {
			content = config.RemoveKey(content, r.Seat, "tools")
		} else {
			content = config.UpsertKey(content, r.Seat, "tools", strconv.Quote(r.New))
		}
		edited[path] = content
		lines = append(lines, fmt.Sprintf("~ %s (%s)", r.file(), r.describe()))
	}
	for _, path := range order {
		if edited[path] == original[path] {
			continue
		}
		if err := os.WriteFile(path, []byte(edited[path]), 0o644); err != nil {
			return err
		}
	}
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
	return nil
}

// driftForeignSeatTools reports each rewrite init would make, naming any tool
// it would leave, so `init --all`'s dry run lists them before --yes applies
// them. A seat with nothing left to rewrite is not drift — init cannot heal it,
// and doctor's agents.binding warning still names it.
func driftForeignSeatTools(repoRoot string) []ScaffoldFinding {
	dataDir := filepath.Join(repoRoot, config.DefaultDataDir)
	if !dirExists(dataDir) {
		return nil
	}
	var out []ScaffoldFinding
	for _, r := range planForeignSeatToolRepairs(dataDir) {
		if !r.rewrites() {
			continue
		}
		detail := r.describe()
		for _, name := range r.Unmapped {
			detail += fmt.Sprintf("; %s has no counterpart there and is left as written", name)
		}
		out = append(out, ScaffoldFinding{Path: r.file(), Kind: "agents", Detail: detail + " — run satelle init"})
	}
	return out
}
