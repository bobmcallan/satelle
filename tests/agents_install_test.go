//go:build integration

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAgentsInstallRemove (sty_aa726901 AC5–AC6): satelle agents install|remove
// is exposed on the binary, is idempotent for all targets, manages only
// satelle-owned launchers, and does not change the configured default reviewer
// or agents.toml / global config.toml.
func TestAgentsInstallRemove(t *testing.T) {
	// Help surfaces the command.
	helpOut := mustRun(t, testBin, t.TempDir(), "agents", "--help")
	if !strings.Contains(helpOut, "install") || !strings.Contains(helpOut, "remove") {
		t.Fatalf("agents --help should name install/remove:\n%s", helpOut)
	}
	// Unknown name lists valid set.
	out, err := run(t, testBin, t.TempDir(), "agents", "install", "nope")
	if err == nil {
		t.Fatalf("unknown name should fail:\n%s", out)
	}
	for _, want := range []string{"claude", "grok", "codex", "all"} {
		if !strings.Contains(out, want) {
			t.Fatalf("error should name %q:\n%s", want, out)
		}
	}

	repo := t.TempDir()
	mustRun(t, testBin, repo, "init")
	mustRun(t, testBin, repo, "reindex")

	agentsToml := filepath.Join(repo, ".satelle", "workflows", "agents.toml")
	beforeAgents, err := os.ReadFile(agentsToml)
	if err != nil {
		t.Fatalf("read agents.toml: %v", err)
	}
	// Global config under isolated SATELLE_HOME (isolatedEnv).
	home := isolatedHome(t)
	globalCfg := filepath.Join(home, "config.toml")
	var beforeGlobal []byte
	if b, err := os.ReadFile(globalCfg); err == nil {
		beforeGlobal = b
	}

	showBefore := mustRun(t, testBin, repo, "agent", "show")

	// install codex → created + executable + marker + adapter
	out = mustRun(t, testBin, repo, "agents", "install", "codex")
	if !strings.Contains(out, "created") && !strings.Contains(out, "unchanged") && !strings.Contains(out, "updated") {
		t.Fatalf("install codex output:\n%s", out)
	}
	if !strings.Contains(out, "No default reviewer") {
		t.Fatalf("install must state default reviewer unchanged:\n%s", out)
	}
	launcher := extractLauncherPath(out, "codex")
	if launcher == "" {
		launcher = filepath.Join(home, "agents", "bin", "satelle-codex")
	}
	assertLauncher(t, launcher, "@agentclientprotocol/codex-acp")

	// Re-run → unchanged, byte-identical.
	beforeBytes, _ := os.ReadFile(launcher)
	out2 := mustRun(t, testBin, repo, "agents", "install", "codex")
	if !strings.Contains(out2, "unchanged") {
		t.Fatalf("second install should be unchanged:\n%s", out2)
	}
	afterBytes, _ := os.ReadFile(launcher)
	if string(beforeBytes) != string(afterBytes) {
		t.Fatal("idempotent install must not rewrite bytes")
	}

	// Global config + agents.toml + agent show unchanged.
	afterAgents, err := os.ReadFile(agentsToml)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeAgents) != string(afterAgents) {
		t.Fatal("agents.toml must be byte-identical after agents install")
	}
	if beforeGlobal != nil {
		afterGlobal, err := os.ReadFile(globalCfg)
		if err != nil {
			t.Fatal(err)
		}
		if string(beforeGlobal) != string(afterGlobal) {
			t.Fatal("global config.toml must be byte-identical after agents install")
		}
	}
	showAfter := mustRun(t, testBin, repo, "agent", "show")
	if showBefore != showAfter {
		t.Fatalf("agent show changed:\nbefore: %s\nafter: %s", showBefore, showAfter)
	}
	if strings.Contains(showAfter, "cli: codex") && !strings.Contains(showBefore, "cli: codex") {
		t.Fatalf("install must not set default agent cli to codex:\n%s", showAfter)
	}

	validateOut := mustRun(t, testBin, repo, "agent", "validate")
	if !strings.Contains(validateOut, "PASS  agent validate green") {
		t.Fatalf("validate after install:\n%s", validateOut)
	}

	// remove codex → gone; re-remove → absent ok
	rem := mustRun(t, testBin, repo, "agents", "remove", "codex")
	if !strings.Contains(rem, "removed") && !strings.Contains(rem, "absent") {
		t.Fatalf("remove:\n%s", rem)
	}
	if _, err := os.Stat(launcher); !os.IsNotExist(err) {
		t.Fatalf("launcher should be removed: %v", err)
	}
	rem2 := mustRun(t, testBin, repo, "agents", "remove", "codex")
	if !strings.Contains(rem2, "absent") && !strings.Contains(rem2, "removed") {
		t.Fatalf("re-remove should be ok:\n%s", rem2)
	}

	// install all → three launchers exist + harness scaffolds (sty_9e86f407)
	allOut := mustRun(t, testBin, repo, "agents", "install", "all")
	for _, name := range []string{"claude", "grok", "codex"} {
		if !strings.Contains(allOut, name) {
			t.Fatalf("install all should mention %s:\n%s", name, allOut)
		}
		p := filepath.Join(home, "agents", "bin", "satelle-"+name)
		assertLauncher(t, p, "")
	}
	// Compliance scaffolds in the repo.
	for _, rel := range []string{
		".claude/settings.json",
		".grok/hooks/satelle.json",
		".codex/hooks.json",
	} {
		p := filepath.Join(repo, filepath.FromSlash(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("scaffold %s: %v", rel, err)
		}
		if !strings.Contains(string(b), "satelle-hook.sh") && !strings.Contains(string(b), "satelle hook") {
			t.Fatalf("scaffold %s missing satelle hook command:\n%s", rel, b)
		}
	}
	// Codex sample binding must not advertise stdio subcommand.
	if strings.Contains(allOut, `stdio`) && strings.Contains(allOut, "command") {
		// Allow comment mention of "no stdio"; fail if command line has it.
		for _, line := range strings.Split(allOut, "\n") {
			if strings.Contains(line, "command") && strings.Contains(line, "=") && strings.Contains(line, "stdio") {
				t.Fatalf("install output must not set command with stdio:\n%s", line)
			}
		}
	}
	if !strings.Contains(allOut, "Compliance") && !strings.Contains(allOut, "engaged") {
		t.Fatalf("install should state compliance / engaged-story guarantee:\n%s", allOut)
	}

	// remove all → three gone
	rmAll := mustRun(t, testBin, repo, "agents", "remove", "all")
	for _, name := range []string{"claude", "grok", "codex"} {
		if !strings.Contains(rmAll, name) {
			t.Fatalf("remove all should mention %s:\n%s", name, rmAll)
		}
		p := filepath.Join(home, "agents", "bin", "satelle-"+name)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s should be removed after remove all", p)
		}
	}

	// Per-target install for each accepted name
	for _, name := range []string{"claude", "grok", "codex"} {
		o := mustRun(t, testBin, repo, "agents", "install", name)
		if !strings.Contains(o, name) {
			t.Fatalf("install %s:\n%s", name, o)
		}
		p := filepath.Join(home, "agents", "bin", "satelle-"+name)
		assertLauncher(t, p, "")
	}
}

func assertLauncher(t *testing.T, path, mustContain string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read launcher %s: %v", path, err)
	}
	if !strings.Contains(string(b), "satelle-owned") {
		t.Fatalf("launcher missing marker: %s\n%s", path, b)
	}
	if mustContain != "" && !strings.Contains(string(b), mustContain) {
		t.Fatalf("launcher %s missing %q:\n%s", path, mustContain, b)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("launcher must be executable: %v mode=%v", err, st.Mode())
	}
}

func extractLauncherPath(out, name string) string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, name) || !strings.Contains(line, "→") {
			continue
		}
		parts := strings.SplitN(line, "→", 2)
		if len(parts) != 2 {
			continue
		}
		rest := strings.TrimSpace(parts[1])
		if i := strings.Index(rest, " "); i > 0 {
			rest = rest[:i]
		}
		if strings.Contains(rest, "satelle-"+name) {
			return rest
		}
	}
	return ""
}
