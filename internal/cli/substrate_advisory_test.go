package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/snapsync"
	"github.com/bobmcallan/satelle/internal/syncstate"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// Substrate continuity advisories (sty_7562055c): the line engagement and
// SessionStart print when substrate is not in step.

const skillsOnlyToml = "[review]\ngate_create = false\n\n[sync]\nskills = \"personal\"\n" + boundProjectToml

// skillsMachine is a checkout that syncs only skills, so a deploy never reaches
// the route files an engaging machine needs.
func skillsMachine(t *testing.T, url string) *snapMachine {
	t.Helper()
	return &snapMachine{t: t, url: url, repo: syncConfigRepo(t, skillsOnlyToml), state: filepath.Join(t.TempDir(), "document-sync-state.json")}
}

// engagingMachine adds a route whose in_progress step is performed by the
// in-loop executor, so `story set --status in_progress` engages without
// dispatching anything. The machine's team set is skills only, so the project
// tier its deploy writes leaves the repo route and agents governing.
func engagingMachine(t *testing.T, url string) *snapMachine {
	t.Helper()
	m := skillsMachine(t, url)
	m.write("satelle.local.toml", "[team]\npaths = [\"skills/**\"]\n")
	writeRoute(t, filepath.Join(m.repo, ".satelle", "workflows"),
		`["*"]
obligations = ["raised", "coded", "closed"]
`,
		`[raised]
status = "backlog"
start = true

[coded]
status = "in_progress"
agent = "executor"
requires = ["raised"]

[closed]
status = "done"
terminal = true
requires = ["coded"]
`)
	m.use()
	t.Chdir(m.repo)
	return m
}

// createStory makes a story on the machine and returns its id.
func (m *snapMachine) createStory() string {
	m.t.Helper()
	m.use()
	m.t.Chdir(m.repo)
	out, err := runRoot(m.t, "story", "create", "--title", "t", "--body", "b", "--acceptance", "1. ok", "--category", "chore")
	if err != nil {
		m.t.Fatalf("create: %v\n%s", err, out)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		m.t.Fatalf("parse create: %v\n%s", err, out)
	}
	return created["id"].(string)
}

// engage runs `story set --status in_progress` and returns the combined output.
func (m *snapMachine) engage(id string) string {
	m.t.Helper()
	m.use()
	m.t.Chdir(m.repo)
	out, err := runRoot(m.t, "story", "set", id, "--status", "in_progress")
	if err != nil {
		m.t.Fatalf("engagement must never be refused for substrate drift: %v\n%s", err, out)
	}
	return out
}

func advisoryLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "substrate not in step") {
			lines = append(lines, l)
		}
	}
	return lines
}

func TestEngagementAdvisoryDriftedInStepAndOffline(t *testing.T) {
	testutil.IsolateHome(t)
	ts, _ := newSnapServer(t)
	seedCred(t, ts.URL)
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	a := skillsMachine(t, ts.URL)
	a.write("skills/x.md", "x\n")
	a.pushConfig()
	b := engagingMachine(t, ts.URL)
	b.deploy()
	id := b.createStory()

	// In step: nothing is printed.
	if lines := advisoryLines(b.engage(id)); len(lines) != 0 {
		t.Fatalf("an in-step machine must print nothing: %v", lines)
	}

	// Drifted: another clone advanced the area and this machine never checked.
	a.write("skills/x.md", "from A\n")
	a.pushConfig()
	out := b.engage(id)
	lines := advisoryLines(out)
	if len(lines) != 1 {
		t.Fatalf("want exactly one advisory line, got %v\n%s", lines, out)
	}
	for _, want := range []string{"skills behind", "satelle sync status"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("advisory %q does not name %q", lines[0], want)
		}
	}
	if strings.Contains(lines[0], "unverified") {
		t.Errorf("a live check must not be marked unverified: %q", lines[0])
	}
	if got := b.recorded()["skills"].State; got != snapsync.StateBehind {
		t.Errorf("engagement must record the state it found, got %q", got)
	}

	// Offline: the server is gone. Local and recorded state speak, marked unverified.
	ts.Close()
	out = b.engage(id)
	lines = advisoryLines(out)
	if len(lines) != 1 || !strings.Contains(lines[0], "skills behind") || !strings.Contains(lines[0], "unverified (offline/unbound)") {
		t.Fatalf("offline advisory = %v\n%s", lines, out)
	}
}

func TestEngagementAdvisoryUnboundMachineIsUnverified(t *testing.T) {
	testutil.IsolateHome(t)
	ts, _ := newSnapServer(t) // no credential seeded: unbound
	if err := config.SaveGlobalHostedServer(ts.URL); err != nil {
		t.Fatal(err)
	}
	b := engagingMachine(t, ts.URL)
	b.write("skills/x.md", "local only\n")
	id := b.createStory()
	lines := advisoryLines(b.engage(id))
	if len(lines) != 1 || !strings.Contains(lines[0], "unverified (offline/unbound)") {
		t.Fatalf("an unbound machine with local-only skills must say so, unverified: %v", lines)
	}
}

func TestEngagementAdvisoryAllLocalIsSilent(t *testing.T) {
	testutil.IsolateHome(t)
	ts, _ := newSnapServer(t)
	m := engagingMachine(t, ts.URL)
	if err := os.WriteFile(filepath.Join(m.repo, ".satelle", "satelle.toml"), []byte("[review]\ngate_create = false\n"+boundProjectToml), 0o644); err != nil {
		t.Fatal(err)
	}
	m.write("skills/x.md", "never synced, but skills is local\n")
	if lines := advisoryLines(m.engage(m.createStory())); len(lines) != 0 {
		t.Fatalf("an all-local repo must stay silent: %v", lines)
	}
}

// substrateHookRepo is a governed repo opted in to sync skills, signed in, with
// a skill and the base it was last synced from.
func substrateHookRepo(t *testing.T, skill, synced string) string {
	t.Helper()
	repo := sessionContextRepo(t)
	hosted.DocumentSyncStatePathOverride = filepath.Join(t.TempDir(), "doc-state.json")
	t.Cleanup(func() { hosted.DocumentSyncStatePathOverride = "" })
	writeHarnessTOML(t, repo, "[sync]\nskills = \"personal\"\nproject = \"p\"\nstale_after = \"24h\"\n")
	seedCred(t, config.DefaultHostedServer)
	data := filepath.Join(repo, config.DefaultDataDir)
	if err := os.MkdirAll(filepath.Join(data, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "skills", "x.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	base := hosted.AreaBase{Version: 1, Files: map[string]string{"skills/x.md": snapsync.SHA([]byte(synced))}}
	if err := hosted.SaveAreaBase(config.DefaultHostedServer, "p", repo, "skills", base); err != nil {
		t.Fatal(err)
	}
	if err := syncstate.RecordSubstrate(config.GlobalDir(), repo, map[string]string{"skills": snapsync.StateInStep}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestHookContextSubstrateAdvisoryDriftedAndInStep(t *testing.T) {
	channel := config.Config{}.SessionContextEvent("grok")

	substrateHookRepo(t, "edited\n", "x\n") // local differs from the last synced base
	_, content, _, emitted := runContext(t, "grok", grokDualPayload(channel, "post_tool_use", "sess-drift"))
	if !emitted {
		t.Fatal("SessionStart context was not emitted")
	}
	lines := advisoryLines(content)
	// The record is fresh, so the line is not marked unverified, and the one
	// drifted area names the command that brings it in step.
	if len(lines) != 1 || !strings.Contains(lines[0], "skills unpushed") || !strings.Contains(lines[0], "run `satelle sync push`") || strings.Contains(lines[0], "unverified") {
		t.Fatalf("drifted SessionStart advisory = %v\n%s", lines, content)
	}
	if !strings.Contains(content, "CONSTITUTION-MARKER-sty_507d3d9c") {
		t.Errorf("the advisory must ride beside the session context, not replace it:\n%s", content)
	}

	substrateHookRepo(t, "x\n", "x\n") // in step
	_, content, _, emitted = runContext(t, "grok", grokDualPayload(channel, "post_tool_use", "sess-instep"))
	if !emitted || len(advisoryLines(content)) != 0 {
		t.Fatalf("an in-step SessionStart must print no advisory: emitted=%v\n%s", emitted, content)
	}
}
