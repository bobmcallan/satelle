package agentstep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// Reviewer tool isolation through the dispatch seam (sty_ef3efb51): a refused
// binding starts no process (AC3/AC7), and each adapter's invocation records the
// system prompt size and the offered-tool figure with its source (AC5).

const isolationVerdict = `{"decision":"accept","notes":"ok"}`

// startCounter writes an executable shim named name that records one line per
// start and prints stdout. The shim IS the injected spawner: its marker file
// counts how many processes a dispatch really started.
func startCounter(t *testing.T, name, stdout string) (path, marker string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, name)
	marker = filepath.Join(dir, "starts")
	script := "#!/bin/sh\necho start >> " + marker + "\ncat > /dev/null\nprintf '%s' '" + stdout + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, marker
}

func starts(marker string) int {
	b, _ := os.ReadFile(marker)
	return strings.Count(string(b), "start")
}

func reviewerInvoke(t *testing.T, iface, command, tools string) InvokeResult {
	t.Helper()
	runner, err := agentcli.RunnerFromBinding(iface, command)
	if err != nil {
		t.Fatal(err)
	}
	g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
	return g.Invoke(context.Background(), InvokeRequest{
		Binding: config.AgentBinding{Tools: tools, Principles: config.PrinciplesNone},
		Section: "reviewer", Rubric: "judge", Payload: map[string]string{"story": "sty_1"},
		Expect: ExpectVerdict, Runner: runner, Skill: "test-skill",
	})
}

// AC3/AC7: a binding whose adapter cannot deny an out-of-grant tool is refused
// before the process starts — start count 0, an error naming adapter and class —
// and the stock grok command preset (always-approve + --tools) starts once.
func TestInvoke_ReviewerRefusalStartsNoProcess(t *testing.T) {
	const ro = "Read,Grep,Glob"
	cases := []struct {
		name, iface, binary, args string
		wantAdapter, wantClass    string
	}{
		{"grok command always-approve, no allow-list", "command", "grok-shim", "-p {payload} --always-approve", "grok/command", "shell"},
		{"grok command always-approve, wider allow-list", "command", "grok-shim", "-p {payload} --tools read_file,run_terminal_command --always-approve", "grok/command", "shell"},
		{"grok acp yolo", "acp", "grok-shim", "agent --always-approve stdio", "grok/acp", "shell"},
		{"grok acp stock spawn: no permission mode, cannot be forced to ask", "acp", "grok-shim", "agent stdio", "grok/acp", "shell"},
		{"claude bypass without allow-list", "command", "claude-shim", "-p --dangerously-skip-permissions", "claude/command", "shell"},
		{"claude stream bypass mode", "stream", "claude-shim", "-p --permission-mode bypassPermissions", "claude/stream", "mcp"},
		{"unrecognised harness", "command", "mystery-shim", "-p {payload}", "unknown/command", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, marker := startCounter(t, tc.binary, isolationVerdict)
			res := reviewerInvoke(t, tc.iface, path+" "+tc.args, ro)
			if res.Err == nil {
				t.Fatalf("binding was not refused: %+v", res)
			}
			msg := res.Err.Error()
			if !strings.Contains(msg, tc.wantAdapter) || !strings.Contains(msg, tc.wantClass) || !strings.Contains(msg, "refused") {
				t.Errorf("refusal %q must name the adapter %q and the tool class %q", msg, tc.wantAdapter, tc.wantClass)
			}
			if n := starts(marker); n != 0 {
				t.Fatalf("a refused reviewer binding started %d process(es)", n)
			}
			if tc.wantAdapter == "grok/acp" && !strings.Contains(msg, "the peer reports no permission mode and cannot be forced to ask") {
				t.Errorf("grok/acp refusal %q must give the reason", msg)
			}
			if res.Decision != nil {
				t.Errorf("a refused dispatch produced a decision: %+v", res.Decision)
			}
		})
	}

	// A scoped grant (Bash(satelle:*)) is not enforced when permissions are
	// skipped: the binding is refused, start count 0, naming claude/<transport>,
	// the shell class and the reason.
	for _, tc := range []struct{ name, iface, args, adapter string }{
		{"claude command bypass + scoped grant", "command", "-p --tools {tools} --dangerously-skip-permissions", "claude/command"},
		{"claude stream bypassPermissions + scoped grant", "stream", "-p --tools {tools} --permission-mode bypassPermissions", "claude/stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, marker := startCounter(t, "claude-shim", isolationVerdict)
			res := reviewerInvoke(t, tc.iface, path+" "+tc.args, ro+",Bash(satelle:*)")
			if res.Err == nil {
				t.Fatalf("binding was not refused: %+v", res)
			}
			msg := res.Err.Error()
			for _, want := range []string{"refused", tc.adapter, "shell", "the specifier is not enforced when permissions are skipped"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal %q must contain %q", msg, want)
				}
			}
			if n := starts(marker); n != 0 {
				t.Fatalf("a refused reviewer binding started %d process(es)", n)
			}
			if res.Decision != nil {
				t.Errorf("a refused dispatch produced a decision: %+v", res.Decision)
			}
		})
	}

	t.Run("stock grok command preset starts", func(t *testing.T) {
		path, marker := startCounter(t, "grok-shim", isolationVerdict)
		command := strings.Replace(agentcli.DefaultGrokCommand, "grok ", path+" ", 1)
		res := reviewerInvoke(t, "command", command, ro)
		if res.Err != nil {
			t.Fatalf("stock grok preset refused: %v", res.Err)
		}
		if n := starts(marker); n != 1 {
			t.Fatalf("start count = %d, want 1", n)
		}
	})
}

// isolation = "operator-attested" (sty_ef3efb51): an unrecognised harness with no
// key is refused (start count 0, the row above); with the key it runs once and the
// invocation records the attested source, a limitation naming the binding and never
// a count. The key waives nothing an adapter decides.
func TestInvoke_OperatorAttestedReviewer(t *testing.T) {
	const ro = "Read,Grep,Glob"
	invoke := func(t *testing.T, iface, command string, b config.AgentBinding) InvokeResult {
		t.Helper()
		runner, err := agentcli.RunnerFromBinding(iface, command)
		if err != nil {
			t.Fatal(err)
		}
		b.Tools, b.Principles = ro, config.PrinciplesNone
		g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
		return g.Invoke(context.Background(), InvokeRequest{
			Binding: b, Section: "judge", Rubric: "judge", Payload: map[string]string{"story": "sty_1"},
			Expect: ExpectVerdict, Runner: runner, Skill: "test-skill",
		})
	}

	t.Run("unrecognised harness without the key: refused, no process", func(t *testing.T) {
		path, marker := startCounter(t, "verdict.sh", isolationVerdict)
		res := invoke(t, "command", path+" -p {payload}", config.AgentBinding{})
		if res.Err == nil || !strings.Contains(res.Err.Error(), "refused") {
			t.Fatalf("want a refusal, got %+v", res)
		}
		if n := starts(marker); n != 0 {
			t.Fatalf("started %d process(es)", n)
		}
	})

	t.Run("unrecognised harness with the key: runs and records the attested source", func(t *testing.T) {
		path, marker := startCounter(t, "verdict.sh", isolationVerdict)
		res := invoke(t, "command", path+" -p {payload} --tools read_file,grep", config.AgentBinding{Isolation: config.IsolationOperatorAttested})
		if res.Err != nil {
			t.Fatalf("attested binding refused: %v", res.Err)
		}
		if n := starts(marker); n != 1 {
			t.Fatalf("start count = %d, want 1", n)
		}
		if res.OfferedToolCount != nil {
			t.Errorf("an attested run recorded a count: %d", *res.OfferedToolCount)
		}
		if res.OfferedToolsSource != "operator-attested" || !strings.Contains(res.IsolationLimitation, `"judge"`) {
			t.Errorf("source/limitation = %q/%q, want operator-attested naming the binding", res.OfferedToolsSource, res.IsolationLimitation)
		}
	})

	t.Run("the key does not admit an adapter's refusal", func(t *testing.T) {
		attested := config.AgentBinding{Isolation: config.IsolationOperatorAttested}
		for _, tc := range []struct{ name, iface, binary, args string }{
			{"grok acp", "acp", "grok-shim", "agent stdio"},
			{"grok command always-approve, no allow-list", "command", "grok-shim", "-p {payload} --always-approve"},
			{"claude bypass, no allow-list", "command", "claude-shim", "-p --dangerously-skip-permissions"},
		} {
			path, marker := startCounter(t, tc.binary, isolationVerdict)
			res := invoke(t, tc.iface, path+" "+tc.args, attested)
			if res.Err == nil || !strings.Contains(res.Err.Error(), "refused") {
				t.Errorf("%s: want a refusal, got %+v", tc.name, res)
			}
			if n := starts(marker); n != 0 {
				t.Errorf("%s: started %d process(es)", tc.name, n)
			}
		}
	})
}

// AC5: every reviewer invocation records system_prompt_bytes and the offered
// tool figure with its source, per adapter. The count is what the harness
// offers (rendered --tools, or the harness's own init report) — never the grant
// length. A grok acp reviewer is refused before it starts (the refusal test).
func TestInvoke_RecordsPromptBytesAndOfferedToolsPerAdapter(t *testing.T) {
	const ro = "Read,Grep,Glob"
	intp := func(p *int) any {
		if p == nil {
			return nil
		}
		return *p
	}

	t.Run("claude command: rendered --tools, source=flag", func(t *testing.T) {
		path, _ := startCounter(t, "claude-shim", `{"result":"{\"decision\":\"accept\",\"notes\":\"ok\"}"}`)
		res := reviewerInvoke(t, "command", strings.Replace(agentcli.DefaultClaudeCommand, "claude ", path+" ", 1), ro)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if res.SystemPromptBytes == 0 {
			t.Error("system_prompt_bytes not recorded")
		}
		if got := intp(res.OfferedToolCount); got != 3 || res.OfferedToolsSource != "flag" || res.IsolationLimitation != "" {
			t.Errorf("isolation = %v/%q/%q, want 3/flag/none", got, res.OfferedToolsSource, res.IsolationLimitation)
		}
	})

	t.Run("claude command: the count is the offered set, not the grant length", func(t *testing.T) {
		path, _ := startCounter(t, "claude-shim", `{"result":"{\"decision\":\"accept\",\"notes\":\"ok\"}"}`)
		res := reviewerInvoke(t, "command", strings.Replace(agentcli.DefaultClaudeCommand, "claude ", path+" ", 1), "Read,Grep,Glob,Bash(satelle:*),Bash(git:*)")
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		// Five grant entries, four distinct tools offered (Bash once).
		if got := intp(res.OfferedToolCount); got != 4 || res.OfferedToolsSource != "flag" {
			t.Errorf("isolation = %v/%q, want 4/flag", got, res.OfferedToolsSource)
		}
	})

	t.Run("claude stream: the harness's own init report wins", func(t *testing.T) {
		skipNoPython(t)
		peer := writeFakeStreamPeer(t)
		runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceStream, peer+" --output-format stream-json")
		if err != nil {
			t.Fatal(err)
		}
		g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
		res := g.Invoke(context.Background(), InvokeRequest{
			// The peer reports two tools; the grant names three: the recorded
			// count must be the reported two.
			Binding: config.AgentBinding{Tools: ro, Principles: config.PrinciplesNone, Env: map[string]string{"STREAM_INIT_TOOLS": "Read,Grep"}},
			Section: "reviewer", Rubric: "judge", Payload: map[string]string{"story": "sty_1"},
			Expect: ExpectVerdict, Runner: runner, Skill: "test-skill",
		})
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if res.SystemPromptBytes == 0 {
			t.Error("system_prompt_bytes not recorded")
		}
		if got := intp(res.OfferedToolCount); got != 2 || res.OfferedToolsSource != "harness" {
			t.Errorf("isolation = %v/%q, want 2/harness", got, res.OfferedToolsSource)
		}
	})

	t.Run("claude stream: no init report falls back to the rendered --tools", func(t *testing.T) {
		skipNoPython(t)
		peer := writeFakeStreamPeer(t)
		runner, err := agentcli.RunnerFromBinding(agentcli.InterfaceStream, peer+" --output-format stream-json")
		if err != nil {
			t.Fatal(err)
		}
		g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
		res := g.Invoke(context.Background(), InvokeRequest{
			Binding: config.AgentBinding{Tools: ro, Principles: config.PrinciplesNone},
			Section: "reviewer", Rubric: "judge", Payload: map[string]string{"story": "sty_1"},
			Expect: ExpectVerdict, Runner: runner, Skill: "test-skill",
		})
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if got := intp(res.OfferedToolCount); got != 3 || res.OfferedToolsSource != "flag" {
			t.Errorf("isolation = %v/%q, want 3/flag", got, res.OfferedToolsSource)
		}
	})

	t.Run("grok command: rendered --tools, source=flag", func(t *testing.T) {
		path, _ := startCounter(t, "grok-shim", isolationVerdict)
		res := reviewerInvoke(t, "command", strings.Replace(agentcli.DefaultGrokCommand, "grok ", path+" ", 1), ro)
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		if res.SystemPromptBytes == 0 {
			t.Error("system_prompt_bytes not recorded")
		}
		if got := intp(res.OfferedToolCount); got != 3 || res.OfferedToolsSource != "flag" {
			t.Errorf("isolation = %v/%q, want 3/flag", got, res.OfferedToolsSource)
		}
	})

}

func skipNoPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
}
