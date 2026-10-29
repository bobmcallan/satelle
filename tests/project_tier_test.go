//go:build integration

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/config"
)

// TestTeamPathReachesTierScriptFromShellRunners (sty_8dedc0a4 AC6): a team-owned
// script is reached through `satelle team path` from the two shells satelle runs
// a command in — `bash -c` (a gate check, agentstep.execCheck) and `sh -c` (a
// trunk proof, trunk.shell) — with nothing in the environment but PATH, and it
// runs the deployed bytes, not the drifted tree copy.
func TestTeamPathReachesTierScriptFromShellRunners(t *testing.T) {
	repo := t.TempDir()
	data := filepath.Join(repo, ".satelle")
	for rel, body := range map[string]string{
		"satelle.toml":              "[hosted]\nproject = \"probe\"\n[team]\npaths = [\"scripts/*.sh\"]\n",
		"scripts/reviewer-token.sh": "#!/bin/sh\necho drifted\n",
	} {
		p := filepath.Join(data, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stamp := config.ProjectTierStamp{Project: "probe", TeamPaths: []string{"scripts/*.sh"}}
	if err := config.WriteProjectTier(runtimeRoot(t, repo), stamp, map[string][]byte{
		"scripts/reviewer-token.sh": []byte("#!/bin/sh\necho team-token\n"),
	}); err != nil {
		t.Fatal(err)
	}
	env := append(isolatedEnv(t), "PATH="+filepath.Dir(testBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, shell := range []string{"bash", "sh"} {
		c := exec.Command(shell, "-c", `"$(satelle team path scripts/reviewer-token.sh)"`)
		c.Dir = repo
		c.Env = env
		out, err := c.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "team-token" {
			t.Errorf("%s -c: %q (%v), want the deployed team-token", shell, out, err)
		}
	}
}
