package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
)

const testPiCommand = "pi -p --tools {tools} --append-system-prompt {system}"

// foreignToolsRepo is an initialised repo with the machine-wide catalog body
// global and the given machine layers (data-dir-relative path → body).
func foreignToolsRepo(t *testing.T, global string, layers map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	if err := runInitTest(t, &strings.Builder{}, repo); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	if err := os.WriteFile(config.GlobalAgentsPath(), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, body := range layers {
		if err := os.WriteFile(filepath.Join(repo, config.DefaultDataDir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func readLayer(t *testing.T, repo, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, config.DefaultDataDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The id4me / satelle-server shape: a profile change moved the planner and the
// coder to the Claude CLI, and the machine layers still grant pi's names. init
// drops the planner's line (its profile grants the same tools) and restates the
// coder's and the workspace seat's in Claude's names, leaving every other line
// as written (sty_f7f80c48 AC1).
func TestInitRepairsForeignSeatTools(t *testing.T) {
	global := `
[profiles.claude-planner]
role    = "agent"
command = "` + agentcli.DefaultClaudeCommand + `"
model   = "opus"
tools   = "Read,Grep,Glob,LS"

[profiles.claude-coder]
role    = "agent"
command = "` + agentcli.DefaultClaudeCommand + `"
model   = "opus"
tools   = "Read,Grep,Glob"
`
	local := `# machine-local seats, written while the profiles ran pi
[planner]
profile = "claude-planner"
tools = "read,grep,find,ls"

[coder]
profile = "claude-coder"
# the coder writes code
tools = "read,bash,edit,write,grep,find,ls"
model = "opus"
`
	workspace := `[auditor]
profile = "claude-coder"
tools = "read,grep"
`
	repo := foreignToolsRepo(t, global, map[string]string{config.LocalAgentsRel: local, config.WorkspaceAgentsRel: workspace})
	dataDir := filepath.Join(repo, config.DefaultDataDir)

	// Precondition: the fixture reaches the real fault — the live loader reads
	// every seat, and the doctor walk flags each one.
	layers, err := config.LoadAgentLayers(dataDir)
	if err != nil {
		t.Fatalf("load layers: %v", err)
	}
	if layers.Local.Agents["planner"].Tools == "" || layers.Local.Agents["coder"].Tools == "" || layers.Workspace.Agents["auditor"].Tools == "" {
		t.Fatalf("the live loader must read the flat seat tables: local %+v workspace %+v", layers.Local.Agents, layers.Workspace.Agents)
	}
	g, err := config.LoadGlobalAgents()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := config.BaselineAgents()
	if err != nil {
		t.Fatal(err)
	}
	eff, _, err := config.ResolveAgentsBaseline(baseline, layers, g)
	if err != nil {
		t.Fatal(err)
	}
	var flagged []string
	for _, s := range agentvalidate.ForeignLayerSeats(layers, eff) {
		flagged = append(flagged, s.Rel+" "+s.Seat)
	}
	want := []string{config.LocalAgentsRel + " coder", config.LocalAgentsRel + " planner", config.WorkspaceAgentsRel + " auditor"}
	if !slices.Equal(flagged, want) {
		t.Fatalf("ForeignLayerSeats = %v, want %v", flagged, want)
	}

	var out strings.Builder
	if err := runInit(&out, repo, false, nil); err != nil {
		t.Fatalf("runInit over the broken seats: %v\n%s", err, out.String())
	}

	wantLocal := `# machine-local seats, written while the profiles ran pi
[planner]
profile = "claude-planner"

[coder]
profile = "claude-coder"
# the coder writes code
tools = "Read,Bash,Edit,Write,Grep,Glob,LS"
model = "opus"
`
	if got := readLayer(t, repo, config.LocalAgentsRel); got != wantLocal {
		t.Errorf("agents.local.toml after init:\n%s\nwant:\n%s", got, wantLocal)
	}
	wantWorkspace := `[auditor]
profile = "claude-coder"
tools = "Read,Grep"
`
	if got := readLayer(t, repo, config.WorkspaceAgentsRel); got != wantWorkspace {
		t.Errorf("agents.workspace.toml after init:\n%s\nwant:\n%s", got, wantWorkspace)
	}
	for _, w := range []string{
		".satelle/workflows/agents.local.toml ([planner] tools read,grep,find,ls → dropped",
		".satelle/workflows/agents.local.toml ([coder] tools read,bash,edit,write,grep,find,ls → Read,Bash,Edit,Write,Grep,Glob,LS",
		".satelle/workflows/agents.workspace.toml ([auditor] tools read,grep → Read,Grep",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("init output must report %q:\n%s", w, out.String())
		}
	}

	// Healed: nothing left to repair, and a second init changes no byte.
	if fs := driftForeignSeatTools(repo); len(fs) != 0 {
		t.Errorf("a repaired repo must carry no foreign-tools drift: %v", fs)
	}
	if err := runInit(&strings.Builder{}, repo, false, nil); err != nil {
		t.Fatal(err)
	}
	if got := readLayer(t, repo, config.LocalAgentsRel); got != wantLocal {
		t.Errorf("a second init must leave the repaired file alone:\n%s", got)
	}
}

// A tool with no counterpart in the resolved adapter's names is left as
// written and reported with the repo, the file, the seat and the name, while
// every tool that has one is restated (sty_f7f80c48 AC2).
func TestInitLeavesUnmappedToolAndReportsIt(t *testing.T) {
	global := `
[profiles.pi-planner]
role    = "agent"
command = "` + testPiCommand + `"
model   = "opus"
`
	local := `# planner moved to pi
[planner]
profile = "pi-planner"
tools = "Read,WebFetch"
`
	repo := foreignToolsRepo(t, global, map[string]string{config.LocalAgentsRel: local})
	if _, unmapped := agentcli.TranslateTools(agentcli.HarnessClaude, agentcli.HarnessPi, "Read,WebFetch"); !slices.Equal(unmapped, []string{"WebFetch"}) {
		t.Fatalf("WebFetch must have no pi counterpart, got unmapped %v", unmapped)
	}
	if fs := driftForeignSeatTools(repo); len(fs) != 1 || !strings.Contains(fs[0].Detail, "WebFetch has no counterpart") {
		t.Errorf("the dry run must list the rewrite and the tool it leaves: %v", fs)
	}

	var out strings.Builder
	if err := runInit(&out, repo, false, nil); err != nil {
		t.Fatalf("runInit: %v\n%s", err, out.String())
	}
	wantLocal := `# planner moved to pi
[planner]
profile = "pi-planner"
tools = "read,WebFetch"
`
	if got := readLayer(t, repo, config.LocalAgentsRel); got != wantLocal {
		t.Errorf("agents.local.toml after init:\n%s\nwant:\n%s", got, wantLocal)
	}
	var report string
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.Contains(l, "WebFetch") && strings.Contains(l, "no counterpart") {
			report = l
		}
	}
	for _, w := range []string{repo, ".satelle/workflows/agents.local.toml", "[planner]", "WebFetch", "pi"} {
		if !strings.Contains(report, w) {
			t.Errorf("the report must name %q: %q\n%s", w, report, out.String())
		}
	}
	// Nothing left that init can rewrite: the seat is no longer drift, so init
	// does not fail on it; doctor's warning still names it.
	if fs := driftForeignSeatTools(repo); len(fs) != 0 {
		t.Errorf("a seat with nothing left to rewrite must not be drift: %v", fs)
	}
}
