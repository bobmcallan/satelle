package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/testutil"
)

const (
	claudeSeatCmd = "claude -p --output-format json --append-system-prompt {system} --allowedTools {tools}"
	grokSeatCmd   = "grok --prompt {system}"
)

// ctxFixture is a repo under an isolated SATELLE_HOME with an authored plane.
type ctxFixture struct {
	t       *testing.T
	repo    string
	dataDir string
}

func newCtxFixture(t *testing.T, agents string) *ctxFixture {
	t.Helper()
	testutil.IsolateHome(t)
	repo := t.TempDir()
	f := &ctxFixture{t: t, repo: repo, dataDir: filepath.Join(repo, ".satelle")}
	f.write("satelle.toml", "")
	if agents != "" {
		f.write("workflows/agents.toml", agents)
	}
	return f
}

func (f *ctxFixture) write(rel, body string) {
	f.t.Helper()
	p := filepath.Join(f.dataDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// principle writes an authored principle, session-resident or on-demand.
func (f *ctxFixture) principle(name, body string, session bool) {
	f.t.Helper()
	tags := "[type:principle]"
	if session {
		tags = "[type:principle, principles:session]"
	}
	f.write("principles/"+name+".md", "---\nname: "+name+"\ntype: principle\ndescription: fixture "+name+"\ntags: "+tags+"\n---\n\n"+body+"\n")
}

func (f *ctxFixture) claudeAgents() string {
	return "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \"" + claudeSeatCmd + "\"\ntools = \"Read,Grep,Glob\"\n"
}

// residentFixture builds a claude-bound repo whose resident set is between the
// claude limit (9500) and the roomiest harness limit (16384): the set that
// passed the old largest-harness ceiling and overflows the harness in use.
func residentFixture(t *testing.T) *ctxFixture {
	t.Helper()
	f := newCtxFixture(t, "")
	f.write("workflows/agents.toml", f.claudeAgents())
	base, _ := renderSessionContext(f.dataDir, harnessBinding{Name: "claude", Limit: 1 << 30, Unit: "bytes"})
	pad := 13000 - len(base)
	if pad < 100 {
		pad = 100
	}
	f.principle("satelle-fixture-big", "# Big\n\n"+strings.Repeat("alpha beta gamma delta ", pad/23+1), true)
	full, _ := renderSessionContext(f.dataDir, harnessBinding{Name: "claude", Limit: 1 << 30, Unit: "bytes"})
	if len(full) <= 9500 || len(full) >= 16384 {
		t.Fatalf("fixture must sit between the claude limit and the largest limit, got %d bytes", len(full))
	}
	return f
}

func TestBoundHarnessesFollowSeatCommands(t *testing.T) {
	f := newCtxFixture(t, "[executor]\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \""+claudeSeatCmd+"\"\n\n[planner]\nrole = \"agent\"\ncommand = \""+grokSeatCmd+"\"\n")
	got, gaps := boundHarnesses(f.dataDir, f.repo)
	if len(gaps) != 0 {
		t.Fatalf("unexpected gaps: %+v", gaps)
	}
	names := map[string]harnessBinding{}
	for _, h := range got {
		names[h.Name] = h
	}
	if len(names) != 2 || names["claude"].Limit != 9500 || names["claude"].Unit != "bytes" {
		t.Fatalf("claude must bind at its SessionStart byte limit, got %+v", names)
	}
	// grok delivers session context on a tool event, clipped in characters.
	if g := names["grok"]; g.Unit != "characters" || g.Limit != config.EmbeddedHarness()["grok"].ToolContextLimitChars {
		t.Fatalf("grok must bind at its tool-event clip, got %+v", g)
	}
}

// An unrecognised command is a gap, never assumed to be any provider.
func TestBoundHarnessesUnrecognisedCommandIsAGap(t *testing.T) {
	f := newCtxFixture(t, "[executor]\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \"mystery-cli -p {system}\"\n")
	got, gaps := boundHarnesses(f.dataDir, f.repo)
	if len(gaps) != 1 || !strings.Contains(gaps[0].Subject, "[reviewer]") ||
		!strings.Contains(gaps[0].Reason, `"mystery-cli" is not detected by any adapter`) {
		t.Fatalf("want a seat gap worded after the failed lookup, got %+v", gaps)
	}
	for _, h := range got {
		for _, seat := range h.Seats {
			if strings.Contains(seat, "[reviewer]") {
				t.Errorf("the unrecognised seat must not bind %s: %+v", h.Name, h)
			}
		}
	}
}

func TestHarnessBudgetGaps(t *testing.T) {
	if _, gap := harnessBudget(config.Config{}, "nosuch"); gap == nil || !strings.Contains(gap.Reason, "no [harness.nosuch] table") {
		t.Fatalf("a harness with no table is a gap naming the table, got %+v", gap)
	}
	cfg := config.Config{Harness: map[string]config.HarnessConfig{
		"zed": {ContextLimitBytes: 100, SessionContextEvent: "PostToolUse"},
	}}
	if _, gap := harnessBudget(cfg, "zed"); gap == nil || !strings.Contains(gap.Reason, "tool_context_limit_chars") {
		t.Fatalf("a tool-event harness with no clip is a gap naming the key, got %+v", gap)
	}
}

// An in-loop seat is the driving session: the harnesses it runs under are the
// ones whose gate wiring is installed.
func TestBoundHarnessesInLoopSeatFollowsInstalledWiring(t *testing.T) {
	f := newCtxFixture(t, "[executor]\ncommand = \"in-loop\"\n")
	if err := os.MkdirAll(filepath.Join(f.repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, ".claude", "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	inLoopSeen := func() bool {
		got, gaps := boundHarnesses(f.dataDir, f.repo)
		if len(gaps) != 0 {
			t.Fatalf("unexpected gaps: %+v", gaps)
		}
		for _, h := range got {
			for _, seat := range h.Seats {
				if h.Name == "claude" && strings.Contains(seat, "in-loop [executor]") {
					return true
				}
			}
		}
		return false
	}
	if !inLoopSeen() {
		t.Fatal("claude must be bound by the in-loop executor once its wiring is installed")
	}
	if err := os.Remove(filepath.Join(f.repo, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	if inLoopSeen() {
		t.Fatal("without installed wiring an in-loop seat binds no harness")
	}
}

// With no agents layer to read, every configured harness is judged.
func TestBoundHarnessesFallBackToEveryConfiguredHarness(t *testing.T) {
	f := newCtxFixture(t, "this is not [valid toml")
	got, gaps := boundHarnesses(f.dataDir, f.repo)
	if len(gaps) != 0 || len(got) < 2 {
		t.Fatalf("got %+v gaps=%+v", got, gaps)
	}
	for _, h := range got {
		if h.Name == "unknown" {
			t.Errorf("the unknown fallback is a budget, not a harness: %+v", h)
		}
	}
}

// The renderer behind the check is the hook's: rendering for one harness at its
// own limit omits what the roomier limit keeps.
func TestRenderSessionContextHonoursTheHarnessLimit(t *testing.T) {
	f := residentFixture(t)
	_, omitted := renderSessionContext(f.dataDir, harnessBinding{Name: "claude", Limit: 9500, Unit: "bytes"})
	if len(omitted) == 0 {
		t.Fatal("the resident set must overflow the 9500-byte harness")
	}
	_, omitted = renderSessionContext(f.dataDir, harnessBinding{Name: "grok", Limit: 16384, Unit: "bytes"})
	if len(omitted) != 0 {
		t.Fatalf("the same set fits 16384 bytes, got omitted %v", omitted)
	}
}

func TestFsDocsPrefersAuthoredOverEmbedded(t *testing.T) {
	f := newCtxFixture(t, "")
	var name string
	for _, d := range config.EmbeddedDefaults() {
		if d.Kind == "principles" {
			name = d.Name
			break
		}
	}
	f.principle(name, "# Authored override", false)
	docs := fsDocs{f.dataDir}
	d, err := docs.Get(context.Background(), "principles", name)
	if err != nil || d.Embedded || !strings.Contains(d.Body, "Authored override") {
		t.Fatalf("the authored file must win: %+v err=%v", d, err)
	}
	if _, err := docs.Get(context.Background(), "principles", "no-such-principle"); err == nil {
		t.Error("an unknown name must be not found")
	}
}
