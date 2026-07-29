//go:build codexlive

// Local Codex smoke (sty_71491143 AC1–AC3). Never runs in make test / make
// integration. It uses the operator's existing Codex CLI login/configuration.
//
// 1) agents install codex → launcher + .codex/hooks.json
// 2) codex exec loads hooks through that hooks.json
// 3) Codex's real PreToolUse path denies a no-story shell mutation
package codexlive

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveCodexInstallHooksAndDeny(t *testing.T) {
	bin := os.Getenv("SATELLE_TEST_BIN")
	if bin == "" {
		var err error
		bin, err = exec.LookPath("satelle")
		if err != nil {
			t.Skip("satelle binary not on PATH; set SATELLE_TEST_BIN")
		}
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex prerequisite: install the codex CLI and log in with `codex login`")
	}
	status := exec.Command("codex", "login", "status")
	if out, err := status.CombinedOutput(); err != nil {
		t.Skipf("Codex prerequisite: authenticate with `codex login` before hook verification: %v\n%s", err, out)
	}
	features := exec.Command("codex", "features", "list")
	if out, err := features.CombinedOutput(); err != nil || !hooksFeatureEnabled(string(out)) {
		t.Skipf("Codex prerequisite: enable the hooks feature before hook verification: %v\n%s", err, out)
	}

	repo := t.TempDir()
	if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	home := filepath.Join(repo, "home")
	cmd := exec.Command(bin, "agents", "install", "codex")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "SATELLE_HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("agents install codex: %v\n%s", err, out)
	}
	hooks := filepath.Join(repo, ".codex", "hooks.json")
	hb, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatalf("expected .codex/hooks.json: %v", err)
	}
	if !strings.Contains(string(hb), "satelle-hook.sh") && !strings.Contains(string(hb), "satelle hook") {
		t.Fatalf("hooks.json missing satelle commands:\n%s", hb)
	}
	if !strings.Contains(string(hb), `"async": false`) {
		t.Fatalf("hooks.json missing required explicit async=false command handlers:\n%s", hb)
	}
	launcher := filepath.Join(home, "agents", "bin", "satelle-codex")
	if st, err := os.Stat(launcher); err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("launcher missing/not executable: %v", err)
	}
	// Replace one installed SessionStart command with a shell-safe absolute probe.
	// The Codex hook contract executes command hooks from the session cwd, and
	// this asserted side effect is deterministic discovery evidence rather than
	// relying only on lifecycle log text.
	probe := filepath.Join(repo, "session-start-probe")
	patched := strings.Replace(string(hb), `"command": "satelle reindex"`, `"command": "touch `+probe+`"`, 1)
	if patched == string(hb) {
		t.Fatal("installed hooks.json has no SessionStart reindex command to probe")
	}
	if err := os.WriteFile(hooks, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use the operator's existing Codex CLI session: an isolated CODEX_HOME would
	// discard its login and turn this into a credential failure instead of a hook
	// verification. --dangerously-bypass-hook-trust makes this unattended without
	// changing the user's hook-trust settings.
	c2 := exec.Command("codex", "exec", "--dangerously-bypass-hook-trust", "--skip-git-repo-check",
		"reply with only the word ok")
	c2.Dir = repo
	c2.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out2, err := c2.CombinedOutput()
	t.Logf("codex exec:\n%s", out2)
	if err != nil {
		t.Fatalf("Codex hook-load probe failed: %v\n%s", err, out2)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Fatalf("installed SessionStart hook did not execute: %v\n%s", err, out2)
	}

	// Policy deny via the exact installed hook wrapper path, with Codex's accepted
	// PreToolUse envelope. The no-story condition is deliberate: the test never
	// engages a Satelle story in this temporary repository.
	editEv := `{"tool_input":{"command":"touch ` + filepath.Join(repo, "denied") + `"},"tool_name":"Bash","hook_event_name":"PreToolUse"}`
	gate := exec.Command(bin, "hook", "gate", "--harness", "codex")
	gate.Dir = repo
	gate.Env = append(os.Environ(), "SATELLE_HOME="+home)
	gate.Stdin = strings.NewReader(editEv)
	var gateOut bytes.Buffer
	gate.Stdout = &gateOut
	if gerr := gate.Run(); gerr == nil {
		t.Fatalf("gate must deny shell mutation with no engaged story; stdout=%s", gateOut.String())
	}
	if !strings.Contains(gateOut.String(), "permissionDecision") || !strings.Contains(gateOut.String(), "deny") {
		t.Fatalf("want Codex deny envelope, got %s", gateOut.String())
	}

	// End-to-end: Codex must invoke the installed PreToolUse gate and prevent the
	// mutation, not merely accept a synthetic event sent directly to Satelle.
	denied := filepath.Join(repo, "should-not-exist")
	c3 := exec.Command("codex", "exec", "--json", "--dangerously-bypass-hook-trust", "--skip-git-repo-check", "--sandbox", "workspace-write",
		"Use your shell tool to run exactly: touch "+denied)
	c3.Dir = repo
	c3.Env = append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out3, err := c3.CombinedOutput()
	t.Logf("codex mutation probe:\n%s", out3)
	if err == nil {
		t.Fatalf("Codex mutation probe unexpectedly succeeded; want PreToolUse denial:\n%s", out3)
	}
	if _, err := os.Stat(denied); err == nil {
		t.Fatalf("Codex created %s despite the no-story PreToolUse gate:\n%s", denied, out3)
	}
	if !strings.Contains(strings.ToLower(string(out3)), "blocked by pretooluse hook") {
		t.Fatalf("Codex did not report the PreToolUse block:\n%s", out3)
	}
}

// hooksFeatureEnabled recognises the `hooks  <maturity>  true|false` row from
// `codex features list`; presence alone is not enough because hooks can be
// explicitly disabled.
func hooksFeatureEnabled(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "hooks" {
			return fields[len(fields)-1] == "true"
		}
	}
	return false
}
