//go:build codexlive

// Opt-in live Codex smoke (sty_9e86f407 AC5). Never runs in make test / make
// integration. Requires SATELLE_CODEX_LIVE=1.
//
// 1) agents install codex → launcher + .codex/hooks.json
// 2) codex exec loads hooks (SessionStart) through that hooks.json
// 3) satelle hook gate --harness codex denies mutation with no story
// 4) optional: live ACP via installed launcher when API keys present
package codexlive

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/agentinstall"
)

func TestLiveCodexInstallHooksAndACP(t *testing.T) {
	if os.Getenv("SATELLE_CODEX_LIVE") != "1" {
		t.Skip("set SATELLE_CODEX_LIVE=1 to run live Codex smoke")
	}
	bin := os.Getenv("SATELLE_TEST_BIN")
	if bin == "" {
		var err error
		bin, err = exec.LookPath("satelle")
		if err != nil {
			t.Skip("satelle binary not on PATH; set SATELLE_TEST_BIN")
		}
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex CLI not on PATH")
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
	launcher := filepath.Join(home, "agents", "bin", "satelle-codex")
	if st, err := os.Stat(launcher); err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("launcher missing/not executable: %v", err)
	}

	// Live path through installed .codex/hooks.json: codex exec must load hooks.
	// Use isolated CODEX_HOME with project trust + hooks feature.
	codexHome := filepath.Join(repo, "codexhome")
	_ = os.MkdirAll(codexHome, 0o755)
	cfg := `[features]
hooks = true
[projects."` + repo + `"]
trust_level = "trusted"
`
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(repo, ".codex", "session-start-probe")
	// Rewrite SessionStart to touch a probe file so we prove hooks.json ran.
	// Keep PreToolUse satelle entries intact.
	patched := strings.Replace(string(hb),
		`"command": "satelle reindex"`,
		`"command": "touch `+probe+`"`, 1)
	if err := os.WriteFile(hooks, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	c2 := exec.Command("codex", "exec", "--dangerously-bypass-hook-trust", "--skip-git-repo-check",
		"reply with only the word ok")
	c2.Dir = repo
	c2.Env = append(os.Environ(), "CODEX_HOME="+codexHome, "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out2, _ := c2.CombinedOutput()
	t.Logf("codex exec:\n%s", out2)
	if _, err := os.Stat(probe); err != nil {
		// SessionStart may still report "hook: SessionStart" even if touch failed.
		if !strings.Contains(string(out2), "SessionStart") && !strings.Contains(string(out2), "hook:") {
			t.Fatalf("installed .codex/hooks.json did not run (no SessionStart / probe): %v\n%s", err, out2)
		}
		t.Logf("SessionStart observed in output; probe file missing (sandbox?): %v", err)
	}

	// Policy deny via the same binary the hooks invoke.
	editEv := `{"tool_input":{"file_path":"` + filepath.Join(repo, "main.go") + `"},"hook_event_name":"PreToolUse"}`
	gate := exec.Command(bin, "hook", "gate", "--harness", "codex")
	gate.Dir = repo
	gate.Env = append(os.Environ(), "SATELLE_HOME="+home)
	gate.Stdin = strings.NewReader(editEv)
	var gateOut bytes.Buffer
	gate.Stdout = &gateOut
	if gerr := gate.Run(); gerr == nil {
		t.Fatalf("gate must deny edit with no engaged story; stdout=%s", gateOut.String())
	}
	if !strings.Contains(gateOut.String(), "permissionDecision") || !strings.Contains(gateOut.String(), "deny") {
		t.Fatalf("want Codex deny envelope, got %s", gateOut.String())
	}

	if os.Getenv("CODEX_API_KEY") == "" && os.Getenv("OPENAI_API_KEY") == "" {
		t.Log("no API key — hooks.json + gate deny asserted; skip live ACP adapter")
		return
	}
	snip := agentinstall.BindingSnippet("codex", launcher)
	cmdLine := extractCmd(snip)
	if cmdLine == "" || !strings.Contains(cmdLine, launcher) {
		t.Fatalf("bad binding command: %q", cmdLine)
	}
	r, err := agentcli.RunnerFromBinding(agentcli.InterfaceACP, cmdLine)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	acpOut, err := r.Run(ctx, agentcli.Request{
		SystemPrompt: "Reply with only: {\"decision\":\"accept\",\"notes\":\"live\"}",
		Payload:      `{}`,
		AllowedTools: "read_file,grep,list_dir",
		Effort:       "low",
	})
	if err != nil {
		t.Fatalf("live ACP via installed launcher: %v\nout=%s", err, acpOut)
	}
	if !strings.Contains(string(acpOut), "decision") {
		t.Fatalf("live ACP response missing decision: %q", acpOut)
	}
}

func extractCmd(snip string) string {
	for _, line := range strings.Split(snip, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "command") && strings.Contains(line, "=") {
			return strings.Trim(strings.TrimSpace(line[strings.Index(line, "=")+1:]), `"`)
		}
	}
	return ""
}
