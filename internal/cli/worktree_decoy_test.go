package cli

// sty_ddbe2669 AC4: a linked worktree outside the main tree carries its own
// satelle.toml, and that copy is not the process of record. After chdir into
// the worktree, the hook entry points and app.Open apply the main tree's
// engagement, gate, vars, exemptions, no-implement rule, and substrate lock.
// Nothing here is handed a process Config built by the test.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/verb"
)

func TestWorktreeDecoyIsNotTheProcessOfRecord(t *testing.T) {
	t.Setenv("SATELLE_HOME", t.TempDir())
	t.Setenv(config.SessionEnv, "")
	_ = os.Unsetenv(config.SessionEnv)
	t.Setenv("SATELLE_CONFIG", "")
	_ = os.Unsetenv("SATELLE_CONFIG")
	t.Setenv("SATELLE_SERVER_ENDPOINT", "none")
	t.Cleanup(verb.ClearEngagementMode)

	base := t.TempDir()
	main := filepath.Join(base, "proj")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, main, "README.md", "x\n")
	gitIn(t, main, "init", "-q")
	gitIn(t, main, "config", "user.email", "t@example.com")
	gitIn(t, main, "config", "user.name", "t")
	gitIn(t, main, "add", "-A")
	gitIn(t, main, "commit", "-q", "-m", "init")
	wt := filepath.Join(base, "wt", "sty_decoy")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, main, "worktree", "add", "-q", "-b", "sty_decoy", wt, "HEAD")

	writeFile(t, main, ".satelle/satelle.toml", mainProcessToml)
	writeFile(t, main, ".satelle/satelle.local.toml", "[vars]\nPROCESS_MARK = \"main-overlay-mark\"\n")
	writeFile(t, main, ".satelle/workflows/agents.toml", mainAgentsToml)
	writeFile(t, main, ".satelle/workflows/done.toml", decoyRouteDone)
	writeFile(t, main, ".satelle/workflows/step.toml", decoyRouteStep)
	writeFile(t, main, ".satelle/constitution.md", "MAIN-PROCESS-CONSTITUTION\n")

	writeFile(t, wt, ".satelle/satelle.toml", decoyProcessToml)
	writeFile(t, wt, ".satelle/workflows/agents.toml", decoyAgentsToml)
	writeFile(t, wt, ".satelle/constitution.md", "DECOY-CONSTITUTION\n")
	writeFile(t, wt, "kept/note.go", "package kept\n")
	writeFile(t, wt, "note.keep", "keep\n")
	writeFile(t, wt, "decoy-kept/note.go", "package decoy\n")
	writeFile(t, wt, "note.decoy", "decoy\n")

	t.Chdir(wt)
	a, err := app.Open()
	if err != nil {
		t.Fatalf("open worktree: %v", err)
	}

	if a.Config.DataDir != "decoy/process" {
		t.Fatalf("location Config.DataDir = %q, want the decoy", a.Config.DataDir)
	}
	if !a.Config.Gate.AllowOutsideTreeEdits {
		t.Fatal("location config is not the decoy: allow_outside_tree_edits should be true there")
	}
	decoyWorkflows := a.Config.ResolveAuthoredDirs(a.RepoRoot)["workflows"]
	if filepath.Clean(decoyWorkflows) != filepath.Join(wt, "decoy-wf", "workflows") {
		t.Fatalf("location authored workflows = %q, want the decoy substrate root", decoyWorkflows)
	}
	if filepath.Clean(a.AuthoredDirs()["workflows"]) == filepath.Clean(decoyWorkflows) {
		t.Fatal("process authored workflows followed the decoy substrate root")
	}
	if got, ok := config.FindDataDir(wt); !ok || got != filepath.Join(wt, config.DefaultDataDir) {
		t.Fatalf("FindDataDir(worktree) = %q, %v", got, ok)
	}
	wantData, err := filepath.EvalSymlinks(filepath.Join(main, config.DefaultDataDir))
	if err != nil {
		t.Fatal(err)
	}
	gotData, err := filepath.EvalSymlinks(a.ProcessDataDir)
	if err != nil {
		t.Fatal(err)
	}
	if gotData != wantData {
		t.Fatalf("ProcessDataDir = %q, want %q", gotData, wantData)
	}
	if a.ProcessConfig.Engagement.Parallel != config.ParallelEpic {
		t.Fatalf("process engagement = %q, want epic", a.ProcessConfig.Engagement.Parallel)
	}
	if a.ProcessConfig.Vars["PROCESS_MARK"] != "main-overlay-mark" {
		t.Fatalf("process var PROCESS_MARK = %q, want the main overlay", a.ProcessConfig.Vars["PROCESS_MARK"])
	}

	if allowOutsideTreeEdits() {
		t.Fatal("allowOutsideTreeEdits applied the decoy true")
	}
	allow := loadCommandAllow()
	if got := strings.Join(allow["push"], ","); got != "release" {
		t.Fatalf("command allow push = %q, want release from the main tree (decoy lists never)", got)
	}
	if exemptTarget("decoy-kept/note.go") || exemptTarget("note.decoy") {
		t.Fatal("exemptTarget honoured a prefix or glob only the decoy lists")
	}
	if !exemptTarget("kept/note.go") || !exemptTarget("note.keep") {
		t.Fatal("exemptTarget missed the main tree's edit_exempt_paths prefix or edit_exempt_globs")
	}

	explain := &cobra.Command{}
	explain.Flags().String("payload", "", "")
	payload := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(payload, []byte(`{"model":"main-blocked-9","tool_input":{"file_path":"internal/x.go"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := explain.Flags().Set("payload", payload); err != nil {
		t.Fatal(err)
	}
	var explained bytes.Buffer
	explain.SetOut(&explained)
	explain.SetErr(&explained)
	if err := runHookExplain(explain, nil); err != nil {
		t.Fatalf("explain: %v\n%s", err, explained.String())
	}
	if !strings.Contains(explained.String(), "decision:   deny") || !strings.Contains(explained.String(), "main model rule") {
		t.Fatalf("explain did not apply the main tree's no_implement_models:\n%s", explained.String())
	}
	if strings.Contains(explained.String(), "decoy model rule") {
		t.Fatalf("explain applied the decoy message:\n%s", explained.String())
	}

	seat := sessionSeatBlock(a)
	if !strings.Contains(seat, "seat mode: epic") {
		t.Fatalf("sessionSeatBlock = %q, want the main tree's epic mode", seat)
	}

	var validated bytes.Buffer
	vcmd := &cobra.Command{}
	vcmd.SetOut(&validated)
	vcmd.SetErr(&validated)
	if err := validateKind(vcmd, a, "workflows", ""); err != nil && !strings.Contains(validated.String(), "main-overlay-mark") {
		t.Fatalf("validate workflows: %v\n%s", err, validated.String())
	}
	if !strings.Contains(validated.String(), `effective_model="main-overlay-mark"`) {
		t.Fatalf("validateKind did not show the main overlay model:\n%s", validated.String())
	}
	if strings.Contains(validated.String(), "unknown var") || strings.Contains(validated.String(), "decoy-agent-model") {
		t.Fatalf("validateKind used the decoy vars or agents:\n%s", validated.String())
	}

	acmd := agentValidateCmd()
	var agentsOut bytes.Buffer
	acmd.SetOut(&agentsOut)
	acmd.SetErr(&agentsOut)
	acmd.SetContext(context.WithValue(context.Background(), appCtxKey{}, a))
	if err := acmd.RunE(acmd, nil); err != nil && !strings.Contains(agentsOut.String(), "main-overlay-mark") {
		t.Fatalf("agent validate: %v\n%s", err, agentsOut.String())
	}
	if !strings.Contains(agentsOut.String(), `model="main-overlay-mark"`) {
		t.Fatalf("agent validate did not show the main overlay model:\n%s", agentsOut.String())
	}
	if strings.Contains(agentsOut.String(), "unknown var") || strings.Contains(agentsOut.String(), "decoy-agent-model") {
		t.Fatalf("agent validate used the decoy vars or agents:\n%s", agentsOut.String())
	}

	grants, _, _ := hookGrantIndex(a, a.ProcessDataDir)
	if grants["reviewer"].Model != "main-overlay-mark" {
		t.Fatalf("hookGrantIndex reviewer model = %q, want the main overlay", grants["reviewer"].Model)
	}

	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	lockCmd := &cobra.Command{}
	var locked bytes.Buffer
	lockCmd.SetOut(&locked)
	lockCmd.SetErr(&locked)
	lerr := substrateLockGate(lockCmd, []byte(`{"tool_input":{"file_path":".satelle/workflows/agents.toml"}}`),
		".satelle/workflows/agents.toml", []seatInfo{{ItemID: "sty_holder", StoryStatus: "in_progress"}})
	if lerr == nil || !strings.Contains(lerr.Error(), "substrate lock") {
		t.Fatalf("substrate lock did not refuse the worktree data dir (the decoy opts out): %v\n%s", lerr, locked.String())
	}

	out, cerr := runRoot(t, "story", "create", "--title", "from the worktree", "--body", "b",
		"--acceptance", "1. a", "--category", "feature")
	if cerr != nil {
		t.Fatalf("story create: %v\n%s", cerr, out)
	}
	if !strings.Contains(out, "created WITHOUT create review") {
		t.Fatalf("create notice did not mention the main tree's ungated create (the decoy sets gate_create true):\n%s", out)
	}

	copyBody, err := os.ReadFile(filepath.Join(wt, ".satelle", "constitution.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(copyBody) != "DECOY-CONSTITUTION\n" {
		t.Fatalf("worktree constitution was rewritten: %q", copyBody)
	}
}

const mainProcessToml = `[engagement]
parallel = "epic"

[gate]
allow_outside_tree_edits = false
edit_exempt_paths = ["kept/"]
edit_exempt_globs = ["*.keep"]
no_implement_models = ["main-blocked*"]
no_implement_message = "main model rule"
command_allow = { push = ["release"] }

[vars]
PUBLIC = "from-committed"
`

const mainAgentsToml = `[reviewer]
role = "reviewer"
model = "main-overlay-mark"
env = { MARKER = "${PROCESS_MARK}" }
`

const decoyRouteDone = `[meta]
name = "done"
type = "workflow"
scope = "project"
description = "feature lane"

[feature]
obligations = ["raised", "planned"]
`

const decoyRouteStep = `[meta]
name = "step"
type = "workflow"
scope = "project"
description = "plan gate on the reviewer binding"

[raised]
status = "backlog"
start = true

[planned]
status = "plan"
reviewers = ["intent"]
reviewer_agent = "reviewer"
requires = ["raised"]
`

const decoyProcessToml = `data_dir = "decoy/process"

[substrate_roots]
workflows = "decoy-wf"
skills = "decoy-skills"
principles = "decoy-principles"
documents = "decoy-documents"

[engagement]
parallel = "none"

[review]
gate_create = true

[gate]
allow_outside_tree_edits = true
edit_exempt_paths = ["decoy-kept/"]
edit_exempt_globs = ["*.decoy"]
no_implement_models = ["decoy-blocked*"]
no_implement_message = "decoy model rule"
lock_substrate_paths = []
command_allow = { push = ["never"] }

[vars]
PROCESS_MARK = "decoy-overlay-value"
PUBLIC = "decoy-committed"
`

const decoyAgentsToml = `[reviewer]
role = "reviewer"
model = "decoy-agent-model"
`
