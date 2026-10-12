package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// localLayerRepo scaffolds a repo whose shared agents.toml is sharedBody and
// whose machine-local override is localBody (empty = no file), with a catalog
// holding the profile "judge" under SATELLE_HOME.
func localLayerRepo(t *testing.T, sharedBody, localBody string) string {
	t.Helper()
	repo := tempRepo(t)
	wfDir := filepath.Join(repo, ".satelle", "workflows")
	writeRoute(t, wfDir, "[\"*\"]\nobligations = [\"raised\", \"closed\"]\n",
		"[raised]\nstatus = \"backlog\"\nstart = true\n\n[closed]\nstatus = \"done\"\nterminal = true\nrequires = [\"raised\"]\n")
	if err := os.WriteFile(filepath.Join(wfDir, config.AgentsConfigName), []byte(sharedBody), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(repo, ".satelle", config.AgentsConfigName))
	if localBody != "" {
		if err := os.WriteFile(filepath.Join(wfDir, config.LocalAgentsName), []byte(localBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	catalog := "[profiles.judge]\nrole = \"reviewer\"\ncommand = \"" + agentcli.DefaultClaudeCommand + "\"\ntools = \"Read,Grep,Glob\"\nmodel = \"catalog-model\"\n"
	if err := os.MkdirAll(config.GlobalDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.GlobalAgentsPath(), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

const localLayerShared = "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[reviewer]\nrole = \"reviewer\"\ncommand = \"" +
	agentcli.DefaultClaudeCommand + "\"\ntools = \"Read,Grep,Glob\"\n"

// A profile= left in the SHARED agents.toml is warned about by both commands,
// naming the binding and the local file to move it into.
func TestSharedProfileWarnedByAgentValidateAndDoctor(t *testing.T) {
	localLayerRepo(t, localLayerShared+"profile = \"judge\"\n", "")
	for _, verb := range []string{"agent validate", "doctor"} {
		out, _ := runRoot(t, strings.Fields(verb)...)
		for _, want := range []string{`[reviewer] declares profile="judge"`, ".satelle/workflows/agents.local.toml"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s must warn on a shared profile=, missing %q:\n%s", verb, want, out)
			}
		}
	}
}

// The same profile= written only in agents.local.toml draws no warning, and a
// field the local file wins reads as local with its path on both commands.
func TestLocalLayerShownByAgentValidateAndDoctor(t *testing.T) {
	localLayerRepo(t, localLayerShared, "[reviewer]\nprofile = \"judge\"\nmodel = \"local-model\"\n")
	for _, verb := range []string{"agent validate", "doctor"} {
		out, err := runRoot(t, strings.Fields(verb)...)
		if verb == "agent validate" && err != nil {
			t.Fatalf("%s: %v\n%s", verb, err, out)
		}
		if strings.Contains(out, "declares profile=") {
			t.Errorf("%s: a profile= only in agents.local.toml must not warn:\n%s", verb, out)
		}
		for _, want := range []string{
			`model = "local-model" ← local (.satelle/workflows/agents.local.toml)`,
			`← repo (.satelle/workflows/agents.toml)`,
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s must show each field's tier and file, missing %q:\n%s", verb, want, out)
			}
		}
	}
}

// `agent validate --help` names the local file, its precedence and that it
// never syncs.
func TestAgentValidateHelpNamesLocalLayer(t *testing.T) {
	out, err := runRoot(t, "agent", "validate", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Precedence:\nagents.local.toml > agents.toml >", "it never\nsyncs"} {
		if !strings.Contains(out, want) {
			t.Errorf("agent validate --help missing %q:\n%s", want, out)
		}
	}
}

// The managed gitignore block that init writes ignores the machine-local file.
func TestGitignoreBlockIgnoresLocalAgents(t *testing.T) {
	repo := t.TempDir()
	if _, err := ensureGitignore(repo); err != nil {
		t.Fatal(err)
	}
	gi, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range strings.Split(string(gi), "\n") {
		found = found || line == ".satelle/workflows/agents.local.toml"
	}
	if !found {
		t.Errorf("managed .gitignore block must ignore .satelle/workflows/agents.local.toml:\n%s", gi)
	}
}
