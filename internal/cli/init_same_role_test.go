package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/health"
)

// sameRoleDeployment lays out a minimal deployed data dir whose agents.toml is
// the given body, under an isolated SATELLE_HOME.
func sameRoleDeployment(t *testing.T, agentsTOML string) string {
	t.Helper()
	t.Setenv("SATELLE_HOME", t.TempDir())
	dataDir := filepath.Join(t.TempDir(), ".satelle")
	if err := os.MkdirAll(filepath.Join(dataDir, "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"satelle.toml":          "",
		"workflows/agents.toml": agentsTOML,
	} {
		if err := os.WriteFile(filepath.Join(dataDir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dataDir
}

const sameRoleReviewer = `[reviewer]
role    = "reviewer"
command = "sh -c --disallowedTools Write,Edit {system} {tools} {model}"
tools   = "Read,Grep,Glob"
`

// sty_29ce243b AC4/AC5: init prints a same-role WARN when the executor and
// orchestrator seats both resolve to in-loop, and that warning never fails init.
func TestValidateDeploymentSameRoleWarnsWithoutFailing(t *testing.T) {
	both := sameRoleDeployment(t, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n[orchestrator]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+sameRoleReviewer)
	var out bytes.Buffer
	_ = validateDeployment(&out, both)
	want := "WARN  [" + health.IDAgentsSameRole + "]"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("init output missing %q:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "FAIL  ["+health.IDAgentsSameRole+"]") {
		t.Fatalf("same-role must never be a FAIL:\n%s", out.String())
	}

	// The warning is not what decides the result: the same layout with only the
	// executor in-loop reaches the same verdict and prints no such line.
	one := sameRoleDeployment(t, "[executor]\nrole = \"agent\"\ncommand = \"in-loop\"\n\n"+sameRoleReviewer)
	var outOne bytes.Buffer
	errOne := validateDeployment(&outOne, one)
	if strings.Contains(outOne.String(), health.IDAgentsSameRole) {
		t.Fatalf("one in-loop seat must not warn:\n%s", outOne.String())
	}
	var outBoth bytes.Buffer
	errBoth := validateDeployment(&outBoth, both)
	if (errOne == nil) != (errBoth == nil) {
		t.Fatalf("the warning changed init's verdict: one-seat err=%v, both-seat err=%v", errOne, errBoth)
	}
}
