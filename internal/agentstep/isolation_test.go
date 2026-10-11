package agentstep

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/config"
)

// Reviewer tool isolation through the dispatch seam (sty_ef3efb51): an
// unsupported binding runs with a warning (sty_2d5e583a), and each adapter's
// invocation records the system prompt size and the offered-tool figure with its
// source (AC5).

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

// namedPeer copies the fake ACP or stream peer under name, so the adapter
// detection (which reads the binary's name) sees a grok, claude or mystery
// harness that actually speaks the transport (a shell stub would wait forever).
func namedPeer(t *testing.T, iface, name string) string {
	t.Helper()
	skipNoPython(t)
	peer := writeFakeACPPeer
	if iface == "stream" {
		peer = writeFakeStreamPeer
	}
	src, err := os.ReadFile(peer(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, src, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// warnedInvoke dispatches a reviewer with the warning writer captured.
func warnedInvoke(t *testing.T, iface, command, tools string, b config.AgentBinding) (InvokeResult, string) {
	t.Helper()
	runner, err := agentcli.RunnerFromBinding(iface, command)
	if err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
	g.SetWarnWriter(&warn)
	b.Tools, b.Principles = tools, config.PrinciplesNone
	res := g.Invoke(context.Background(), InvokeRequest{
		Binding: b, Section: "reviewer", Rubric: "judge", Payload: map[string]string{"story": "sty_1"},
		Expect: ExpectVerdict, Runner: runner, Skill: "test-skill",
	})
	return res, warn.String()
}

// sty_2d5e583a AC1/AC2: a binding no adapter path can hold to its grant runs —
// the process starts and returns its verdict — with ONE warning on stderr naming
// the binding, the adapter and the gap, and the limitation and offered-tools
// source recorded on the invocation. It is never refused.
func TestInvoke_UnsupportedReviewerRunsWithWarning(t *testing.T) {
	const ro = "Read,Grep,Glob"
	cases := []struct {
		name, iface, binary, args, tools string
		wantAdapter, wantGap             string
	}{
		{"grok command always-approve, no allow-list", "command", "grok-shim", "-p {payload} --always-approve", ro, "grok/command", "tools not held to the grant"},
		{"grok command always-approve, wider allow-list", "command", "grok-shim", "-p {payload} --tools read_file,run_terminal_command --always-approve", ro, "grok/command", "tools not held to the grant"},
		{"grok acp yolo", "acp", "grok-shim", "agent --always-approve stdio", ro, "grok/acp", "usage accounting not to standard"},
		{"grok acp stock spawn", "acp", "grok-shim", "agent stdio", ro, "grok/acp", "tools not held to the grant"},
		{"claude bypass without allow-list", "command", "claude-shim", "-p --dangerously-skip-permissions", ro, "claude/command", "tools not held to the grant"},
		{"claude stream bypass mode", "stream", "claude-shim", "-p --permission-mode bypassPermissions", ro, "claude/stream", "tools not held to the grant"},
		{"claude command bypass + scoped grant", "command", "claude-shim", "-p --tools {tools} --dangerously-skip-permissions", ro + ",Bash(satelle:*)", "claude/command", "scoped grant not enforced"},
		{"claude stream bypassPermissions + scoped grant", "stream", "claude-shim", "-p --tools {tools} --permission-mode bypassPermissions", ro + ",Bash(satelle:*)", "claude/stream", "scoped grant not enforced"},
		{"unrecognised harness", "command", "mystery-shim", "-p {payload}", ro, "unknown/command", "tools not held to the grant"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var command, marker string
			if tc.iface != "command" {
				command = namedPeer(t, tc.iface, tc.binary) + " " + tc.args
			} else {
				var path string
				path, marker = startCounter(t, tc.binary, isolationVerdict)
				command = path + " " + tc.args
			}
			res, warn := warnedInvoke(t, tc.iface, command, tc.tools, config.AgentBinding{})
			if res.Err != nil {
				t.Fatalf("the binding was refused: %v", res.Err)
			}
			if res.Decision == nil {
				t.Fatalf("the process ran but no verdict came back: %+v", res)
			}
			if marker != "" {
				if n := starts(marker); n != 1 {
					t.Fatalf("start count = %d, want 1", n)
				}
			}
			if n := strings.Count(warn, "warning:"); n != 1 {
				t.Fatalf("want exactly one warning line, got %d: %q", n, warn)
			}
			for _, want := range []string{`"reviewer"`, tc.wantAdapter, tc.wantGap, "fix:"} {
				if !strings.Contains(warn, want) {
					t.Errorf("warning %q must contain %q", warn, want)
				}
			}
			if !strings.Contains(res.IsolationLimitation, tc.wantGap) {
				t.Errorf("isolation_limitation = %q, want it to carry %q", res.IsolationLimitation, tc.wantGap)
			}
			if res.OfferedToolsSource == "" {
				t.Error("offered_tools_source must never be empty")
			}
		})
	}

	t.Run("grok acp names both gaps and records offered tools as unavailable", func(t *testing.T) {
		res, warn := warnedInvoke(t, "acp", namedPeer(t, "acp", "grok-shim")+" agent stdio", ro, config.AgentBinding{})
		if res.Err != nil {
			t.Fatal(res.Err)
		}
		for _, want := range []string{"tools not held to the grant", "usage accounting not to standard"} {
			if !strings.Contains(warn, want) {
				t.Errorf("warning %q must name %q", warn, want)
			}
		}
		if !strings.HasPrefix(res.OfferedToolsSource, "unavailable: grok") {
			t.Errorf("offered_tools_source = %q, want an adapter-named unavailable reason", res.OfferedToolsSource)
		}
	})

	t.Run("stock grok command preset runs with no warning and no limitation", func(t *testing.T) {
		path, marker := startCounter(t, "grok-shim", isolationVerdict)
		command := strings.Replace(agentcli.DefaultGrokCommand, "grok ", path+" ", 1)
		res, warn := warnedInvoke(t, "command", command, ro, config.AgentBinding{})
		if res.Err != nil {
			t.Fatalf("stock grok preset refused: %v", res.Err)
		}
		if n := starts(marker); n != 1 {
			t.Fatalf("start count = %d, want 1", n)
		}
		if warn != "" || res.IsolationLimitation != "" {
			t.Errorf("stock preset warned %q / limited %q", warn, res.IsolationLimitation)
		}
	})
}

// sty_2d5e583a AC2: an observed mid-session breach — a write tool that ran without
// a permission request — is warned and ledgered, and the run still returns its
// verdict. An operator-attested binding is still warned of it: a breach is seen
// behaviour, not a configured gap.
func TestInvoke_ObservedBreachIsWarnedAndLedgeredNeverAborts(t *testing.T) {
	skipNoPython(t)
	src, err := os.ReadFile(writeFakeACPPeer(t))
	if err != nil {
		t.Fatal(err)
	}
	const marker = "    elif method == \"session/prompt\":\n"
	breach := marker + `        send({"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess_test","update":{"sessionUpdate":"tool_call","toolCallId":"c1","kind":"edit","title":"write_file","status":"in_progress"}}})` + "\n"
	script := strings.Replace(string(src), marker, breach, 1)
	if script == string(src) {
		t.Fatal("the fake ACP peer no longer has a session/prompt branch to inject the breach into")
	}
	for name, b := range map[string]config.AgentBinding{
		"unattested": {},
		"attested":   {Isolation: config.IsolationOperatorAttested},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "grok-shim")
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			runner, err := agentcli.RunnerFromBinding("acp", path+" agent stdio")
			if err != nil {
				t.Fatal(err)
			}
			var warn bytes.Buffer
			g := New(&fakeRunner{}, fakeDocs{}, t.TempDir(), "")
			g.SetWarnWriter(&warn)
			recs := captureTelemetry(g)
			b.Tools, b.Principles = "Read,Grep,Glob", config.PrinciplesNone
			res := g.Invoke(context.Background(), InvokeRequest{
				Binding: b, Section: "reviewer", Rubric: "judge", Payload: map[string]string{"story": "sty_1"},
				Expect: ExpectVerdict, Runner: runner, Skill: "test-skill",
			})
			if res.Err != nil || res.Decision == nil {
				t.Fatalf("a breach must not abort the run: %+v", res)
			}
			if countKind(*recs, "reviewer-isolation-breach") != 1 {
				t.Errorf("want one reviewer-isolation-breach event: %+v", *recs)
			}
			if !strings.Contains(warn.String(), "write_file") {
				t.Errorf("the breach warning must name the tool: %q", warn.String())
			}
		})
	}
}

// isolation = "operator-attested" (sty_2d5e583a): the operator's acknowledgement.
// An unrecognised harness runs with or without it; with it the configured-gap
// warning is suppressed and the ledger limitation is still recorded — naming the
// binding, never a count. It acknowledges every gap, an adapter's included.
func TestInvoke_OperatorAttestedReviewer(t *testing.T) {
	const ro = "Read,Grep,Glob"
	attested := config.AgentBinding{Isolation: config.IsolationOperatorAttested}

	t.Run("unrecognised harness without the key: runs, warns", func(t *testing.T) {
		path, marker := startCounter(t, "verdict.sh", isolationVerdict)
		res, warn := warnedInvoke(t, "command", path+" -p {payload}", ro, config.AgentBinding{})
		if res.Err != nil {
			t.Fatalf("refused: %v", res.Err)
		}
		if n := starts(marker); n != 1 {
			t.Fatalf("start count = %d, want 1", n)
		}
		if strings.Count(warn, "warning:") != 1 {
			t.Errorf("want one warning, got %q", warn)
		}
	})

	t.Run("unrecognised harness with the key: no warning, records the attested source", func(t *testing.T) {
		path, marker := startCounter(t, "verdict.sh", isolationVerdict)
		res, warn := warnedInvoke(t, "command", path+" -p {payload} --tools read_file,grep", ro, attested)
		if res.Err != nil {
			t.Fatalf("attested binding refused: %v", res.Err)
		}
		if n := starts(marker); n != 1 {
			t.Fatalf("start count = %d, want 1", n)
		}
		if warn != "" {
			t.Errorf("an attested binding warned: %q", warn)
		}
		if res.OfferedToolCount != nil {
			t.Errorf("an attested run recorded a count: %d", *res.OfferedToolCount)
		}
		if res.OfferedToolsSource != "operator-attested" || !strings.Contains(res.IsolationLimitation, `"reviewer"`) {
			t.Errorf("source/limitation = %q/%q, want operator-attested naming the binding", res.OfferedToolsSource, res.IsolationLimitation)
		}
	})

	t.Run("the key acknowledges an adapter's gap: no warning, limitation still recorded", func(t *testing.T) {
		for _, tc := range []struct{ name, iface, binary, args string }{
			{"grok acp", "acp", "grok-shim", "agent stdio"},
			{"grok command always-approve, no allow-list", "command", "grok-shim", "-p {payload} --always-approve"},
			{"claude bypass, no allow-list", "command", "claude-shim", "-p --dangerously-skip-permissions"},
		} {
			var command string
			if tc.iface != "command" {
				command = namedPeer(t, tc.iface, tc.binary) + " " + tc.args
			} else {
				path, _ := startCounter(t, tc.binary, isolationVerdict)
				command = path + " " + tc.args
			}
			res, warn := warnedInvoke(t, tc.iface, command, ro, attested)
			if res.Err != nil || res.Decision == nil {
				t.Errorf("%s: want a verdict, got %+v", tc.name, res)
			}
			if warn != "" {
				t.Errorf("%s: an attested binding warned: %q", tc.name, warn)
			}
			if res.IsolationLimitation == "" {
				t.Errorf("%s: the ledger limitation must still be recorded", tc.name)
			}
		}
	})
}

// A pi binding is a recognised adapter (sty_58a9bdc8): it is never the
// unknown-adapter gap, and an operator-attested declaration on it is redundant —
// the ledger records pi's own offered tools and gap, not the attested source.
func TestInvoke_PiReviewerIsRecognised(t *testing.T) {
	const ro = "read,grep,find,ls"
	attested := config.AgentBinding{Isolation: config.IsolationOperatorAttested}

	t.Run("restricted pi command: no warning, offered tools from the flag", func(t *testing.T) {
		path, marker := startCounter(t, "pi", isolationVerdict)
		res, warn := warnedInvoke(t, "command", path+" -p {payload} --tools read,grep,find,ls", ro, config.AgentBinding{})
		if res.Err != nil || starts(marker) != 1 {
			t.Fatalf("err = %v starts = %d", res.Err, starts(marker))
		}
		if warn != "" {
			t.Errorf("a restricted pi binding warned: %q", warn)
		}
		if res.OfferedToolCount == nil || *res.OfferedToolCount != 4 || res.OfferedToolsSource != agentcli.OfferedSourceFlag {
			t.Errorf("offered = %v (%q), want 4 via flag", res.OfferedToolCount, res.OfferedToolsSource)
		}
	})

	t.Run("unrestricted pi command: a pi-named gap, never the unknown-adapter one", func(t *testing.T) {
		path, _ := startCounter(t, "pi", isolationVerdict)
		res, warn := warnedInvoke(t, "command", path+" -p {payload}", ro, config.AgentBinding{})
		if res.Err != nil {
			t.Fatalf("refused: %v", res.Err)
		}
		if strings.Count(warn, "warning:") != 1 || !strings.Contains(warn, "pi/command") || strings.Contains(warn, "no adapter knows") {
			t.Errorf("warning = %q, want one pi/command warning", warn)
		}
		if !strings.Contains(res.IsolationLimitation, "pi/command") || res.OfferedToolCount != nil {
			t.Errorf("limitation = %q count = %v, want the pi gap and no count", res.IsolationLimitation, res.OfferedToolCount)
		}
	})

	t.Run("operator-attested pi: the adapter's description, not the attested source", func(t *testing.T) {
		path, _ := startCounter(t, "pi", isolationVerdict)
		res, _ := warnedInvoke(t, "command", path+" -p {payload} --tools read,grep,find,ls", ro, attested)
		if res.Err != nil {
			t.Fatalf("refused: %v", res.Err)
		}
		if res.OfferedToolsSource != agentcli.OfferedSourceFlag || res.OfferedToolCount == nil || *res.OfferedToolCount != 4 {
			t.Errorf("source = %q count = %v, want pi's flag-sourced 4 tools, not %q", res.OfferedToolsSource, res.OfferedToolCount, agentcli.OfferedSourceAttested)
		}
	})
}

// AC5: every reviewer invocation records system_prompt_bytes and the offered
// tool figure with its source, per adapter. The count is what the harness
// offers (rendered --tools, or the harness's own init report) — never the grant
// length. A grok acp reviewer runs with a warning and records an unverified
// source (the warning test).
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
