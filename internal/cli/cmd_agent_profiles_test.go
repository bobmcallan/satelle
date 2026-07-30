package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentvalidate"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/testutil"
)

// TestAgentProfilesEmptyHomeGuidesTheOperator pins the zero-config surface: with
// no catalog, `satelle agent profiles` says so and names the migration path
// rather than failing or printing an empty table.
func TestAgentProfilesEmptyHomeGuidesTheOperator(t *testing.T) {
	testutil.IsolateHome(t)
	cmd, buf := testCmd()
	cmd.SetOut(buf)
	if err := agentProfilesCmd().RunE(cmd, nil); err != nil {
		t.Fatalf("agent profiles: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"no profiles defined", "satelle agent migrate"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should carry %q: %q", want, out)
		}
	}
}

// TestAgentProfilesListsCatalogWithoutSecrets pins the catalog display: profiles
// and opt-in role defaults are listed, env KEY names appear, and no env VALUE
// ever does.
func TestAgentProfilesListsCatalogWithoutSecrets(t *testing.T) {
	testutil.IsolateHome(t)
	const secret = "sk-never-print-me"
	writeCatalog(t, `
[vars]
TOKEN = "`+secret+`"

[profiles.claude-opus]
role    = "reviewer"
command = "claude -p --append-system-prompt {system} --model {model}"
tools   = "Read,Grep,Glob"
model   = "opus"
env     = { ANTHROPIC_AUTH_TOKEN = "${TOKEN}" }

[roles]
reviewer = "claude-opus"
`)
	cmd, buf := testCmd()
	cmd.SetOut(buf)
	if err := agentProfilesCmd().RunE(cmd, nil); err != nil {
		t.Fatalf("agent profiles: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"claude-opus", "opus", "ANTHROPIC_AUTH_TOKEN", "ROLE DEFAULT", "use_global_roles"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should carry %q: %q", want, out)
		}
	}
	if strings.Contains(out, secret) {
		t.Error("a var VALUE must never be printed")
	}
}

// TestAgentMigrateIsOptInAndNonDestructive pins AC6: migrate seeds a starter
// catalog from the machine's selected CLI, refuses to overwrite an existing one,
// and writes nothing into any repository.
func TestAgentMigrateIsOptInAndNonDestructive(t *testing.T) {
	home := testutil.IsolateHome(t)
	repo := t.TempDir()
	repoAgents := filepath.Join(repo, config.AgentsConfigName)
	const repoBody = "[reviewer]\nrole = \"reviewer\"\n"
	if err := os.WriteFile(repoAgents, []byte(repoBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd, buf := testCmd()
	cmd.SetOut(buf)
	if err := agentMigrateCmd().RunE(cmd, nil); err != nil {
		t.Fatalf("agent migrate: %v", err)
	}
	seeded, err := os.ReadFile(filepath.Join(home, config.GlobalAgentsName))
	if err != nil {
		t.Fatalf("migrate must seed the catalog: %v", err)
	}
	if _, perr := config.ParseGlobalAgents(string(seeded)); perr != nil {
		t.Fatalf("seeded catalog must load: %v", perr)
	}
	// The repo is untouched — migration is a machine-side action.
	if got, _ := os.ReadFile(repoAgents); string(got) != repoBody {
		t.Errorf("migrate must not write into a repository: %q", string(got))
	}

	// A second run refuses to overwrite; the file is byte-identical.
	cmd, buf = testCmd()
	cmd.SetOut(buf)
	if err := agentMigrateCmd().RunE(cmd, nil); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if !strings.Contains(buf.String(), "never overwrites") {
		t.Errorf("second run should refuse to overwrite: %q", buf.String())
	}
	again, _ := os.ReadFile(filepath.Join(home, config.GlobalAgentsName))
	if !bytes.Equal(seeded, again) {
		t.Error("an existing catalog must survive a second migrate byte-for-byte")
	}
}

// TestGrantSourcesRenderWithoutSecretValues pins AC4's display contract at the
// CLI: each effective field prints with its source tier, and env/settings print
// their source WITHOUT the values.
func TestGrantSourcesRenderWithoutSecretValues(t *testing.T) {
	g := agentvalidate.Grant{
		Name:      "reviewer",
		Command:   "claude -p {system}",
		Tools:     "Read,Grep,Glob",
		Model:     "opus",
		Effort:    "low",
		Interface: "command",
		Role:      "reviewer",
		Sources: map[string]string{
			"command":  config.SourceProfile("claude-opus"),
			"tools":    config.SourceEmbedded,
			"model":    config.SourceGlobalRole("claude-opus"),
			"effort":   config.SourceRepo,
			"env":      config.SourceProfile("claude-opus"),
			"settings": config.SourceRepo,
		},
	}
	buf := &bytes.Buffer{}
	printGrantSources(buf, g)
	out := buf.String()
	for _, want := range []string{
		`command = "claude -p {system}" (profile:claude-opus)`,
		`tools = "Read,Grep,Glob" (embedded)`,
		`model = "opus" (global-role:claude-opus)`,
		`effort = "low" (repo)`,
		"env (profile:claude-opus) — values withheld",
		"settings (repo) — values withheld",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing source line %q in:\n%s", want, out)
		}
	}
	// A grant with no provenance (the catalog-free path) renders nothing extra.
	buf.Reset()
	printGrantSources(buf, agentvalidate.Grant{Name: "reviewer"})
	if buf.Len() != 0 {
		t.Errorf("an unsourced grant must print no source lines: %q", buf.String())
	}
}

// TestGlobalHomeSubstrateNeverBecomesRepoWorkflows pins AC7 at the surface that
// would notice: workflow discovery reads the repo's .satelle/workflows and the
// embedded defaults only. A global home stuffed with workflows/ and skills/
// changes nothing about which workflows a repo has.
func TestGlobalHomeSubstrateNeverBecomesRepoWorkflows(t *testing.T) {
	home := testutil.IsolateHome(t)
	dataDir := filepath.Join(t.TempDir(), ".satelle")
	wfDir := filepath.Join(dataDir, "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	repoWf := "---\nname: repo-flow\napplies_to: [\"*\"]\n---\n\n```dot\ndigraph f {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare]\n  backlog -> done\n}\n```\n"
	if err := os.WriteFile(filepath.Join(wfDir, "repo-flow.md"), []byte(repoWf), 0o644); err != nil {
		t.Fatal(err)
	}
	before := deployedWorkflowDocs(dataDir)

	// Now stuff the machine-wide home with things that look like process.
	for _, kind := range []string{"workflows", "skills", "principles"} {
		dir := filepath.Join(home, kind)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: hijack\napplies_to: [\"*\"]\n---\n\n```dot\ndigraph h {\n  backlog [shape=Mdiamond]\n  done [shape=Msquare]\n  backlog -> done\n}\n```\n"
		if err := os.WriteFile(filepath.Join(dir, "hijack.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCatalog(t, "[profiles.p]\nrole = \"reviewer\"\ncommand = \"claude -p {system}\"\n")

	after := deployedWorkflowDocs(dataDir)
	if len(before) != len(after) || len(after) != 1 || after[0].Name != "repo-flow" {
		t.Fatalf("workflow discovery must stay repo-only: before=%d after=%+v", len(before), after)
	}
}

// writeCatalog writes a machine-wide profile catalog into the isolated home.
func writeCatalog(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(config.GlobalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.GlobalAgentsPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
