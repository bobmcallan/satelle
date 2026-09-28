package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolation = "operator-attested" (sty_ef3efb51) is the one valid isolation value;
// anything else is refused at load, in the repo file and the machine catalog alike.
func TestBindingIsolationKey(t *testing.T) {
	load := func(t *testing.T, body string) (AgentsConfig, error) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, AgentsConfigName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return LoadAgents(dir)
	}

	ac, err := load(t, "[reviewer]\ncommand = \"verdict.sh -p {payload}\"\nisolation = \"operator-attested\"\n")
	if err != nil {
		t.Fatalf("valid isolation refused: %v", err)
	}
	if !ac.Reviewer.OperatorAttested() {
		t.Error("isolation = operator-attested did not load")
	}
	if ac, err = load(t, "[reviewer]\ncommand = \"verdict.sh -p {payload}\"\n"); err != nil || ac.Reviewer.OperatorAttested() {
		t.Errorf("an absent key must not attest: err=%v attested=%v", err, ac.Reviewer.OperatorAttested())
	}

	for _, bad := range []string{"trusted", "none", "operator"} {
		_, err := load(t, "[reviewer]\ncommand = \"verdict.sh -p {payload}\"\nisolation = \""+bad+"\"\n")
		if err == nil || !strings.Contains(err.Error(), "isolation") || !strings.Contains(err.Error(), IsolationOperatorAttested) {
			t.Errorf("isolation %q: want a load error naming the valid value, got %v", bad, err)
		}
		if _, err := ParseGlobalAgents("[profiles.p]\ncommand = \"verdict.sh -p {payload}\"\nisolation = \"" + bad + "\"\n"); err == nil {
			t.Errorf("catalog isolation %q must be refused", bad)
		}
	}

	g, err := ParseGlobalAgents("[profiles.p]\ncommand = \"verdict.sh -p {payload}\"\nisolation = \"operator-attested\"\n")
	if err != nil {
		t.Fatalf("catalog isolation refused: %v", err)
	}
	if !g.Profiles["p"].OperatorAttested() {
		t.Error("catalog isolation did not load")
	}
}
