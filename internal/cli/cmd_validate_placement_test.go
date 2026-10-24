package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A binding's placement (sty_7028d996) in the validate and show surfaces:
// `satelle validate` refuses a value that is not local|remote|team:<path>, and
// validate, `agent validate` and `workflow show` print each binding's placement.

const placementAgents = `[executor]
harness = "in-loop"

[plain]
role = "agent"
command = "claude -p --append-system-prompt {system}"

[far]
role = "agent"
command = "claude -p --append-system-prompt {system}"
placement = "remote"
remote = "r1"

[team]
role = "agent"
placement = "team:acme/ops"
`

func writeRepoAgents(t *testing.T, repo, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".satelle", "agents.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AC1: a placement that is not local, remote or team:<path> fails satelle
// validate, naming the value and what is accepted.
func TestValidateRefusesAnUnknownPlacement(t *testing.T) {
	for name, line := range map[string]string{
		"unknown":      `placement = "bogus"`,
		"team no path": `placement = "team:"`,
		"remote no id": `placement = "remote"`,
	} {
		t.Run(name, func(t *testing.T) {
			repo := tempRepo(t)
			t.Chdir(repo)
			writeRepoAgents(t, repo, "[executor]\nharness = \"in-loop\"\n\n[worker]\nrole = \"agent\"\ncommand = \"claude -p {system}\"\n"+line+"\n")
			out, err := runRootIn(t, "", "validate")
			if err == nil {
				t.Fatalf("validate passed with %s:\n%s", line, out)
			}
			got := err.Error() + out
			if !strings.Contains(got, "placement") {
				t.Errorf("validate output does not name the placement:\n%s", got)
			}
		})
	}
}

// AC1: the refusal names the value and what is accepted, and the exit is non-zero.
func TestValidateNamesTheBadPlacement(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRepoAgents(t, repo, "[executor]\nharness = \"in-loop\"\n\n[worker]\nrole = \"agent\"\ncommand = \"claude -p {system}\"\nplacement = \"bogus\"\n")
	out, err := runRootIn(t, "", "validate")
	if err == nil {
		t.Fatalf("validate passed:\n%s", out)
	}
	if got := err.Error() + out; !strings.Contains(got, "placement=bogus is not local|remote|team:<path>") {
		t.Errorf("want a refusal naming placement=bogus and the accepted values, got:\n%s", got)
	}
}

// AC4: validate prints each effective binding's placement.
func TestValidatePrintsEachBindingsPlacement(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRepoAgents(t, repo, placementAgents)
	out, err := runRootIn(t, "", "validate")
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	for _, want := range []string{
		"BINDING [plain] placement=local",
		"BINDING [far] placement=remote",
		"BINDING [team] placement=team:acme/ops",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("validate output lacks %q:\n%s", want, out)
		}
	}
}

// AC4: `agent validate` carries the same placement on each GRANT line.
func TestAgentValidateGrantLinesCarryPlacement(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	writeRepoAgents(t, repo, placementAgents)
	out, err := runRootIn(t, "", "agent", "validate")
	if err != nil {
		t.Fatalf("agent validate: %v\n%s", err, out)
	}
	for name, want := range map[string]string{"plain": "placement=local", "far": "placement=remote", "team": "placement=team:acme/ops"} {
		line := ""
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, "GRANT ["+name+"]") {
				line = ln
			}
		}
		if !strings.Contains(line, want) {
			t.Errorf("GRANT [%s] line lacks %q: %q", name, want, line)
		}
	}
}

// AC5: a repo with no placement field validates exactly as before and reports
// every binding local.
func TestValidateWithNoPlacementReportsLocal(t *testing.T) {
	repo := tempRepo(t)
	t.Chdir(repo)
	out, err := runRootIn(t, "", "validate")
	if err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	if strings.Contains(out, "placement=remote") || strings.Contains(out, "placement=team") {
		t.Errorf("a repo declaring no placement reported a non-local one:\n%s", out)
	}
	if !strings.Contains(out, "placement=local") {
		t.Errorf("validate printed no binding placement:\n%s", out)
	}
}
