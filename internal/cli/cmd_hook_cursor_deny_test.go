package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bobmcallan/satelle/internal/agentcli"
	"github.com/bobmcallan/satelle/internal/help"
)

// sty_be756616: the PreToolUse deny encodings moved to internal/agentcli, cursor
// gained one, and the installed wrapper learned to pass it through. The literals
// below were captured from the pre-change implementation, so they pin that
// claude, grok and pi did not move.

// preChangeInfraReason is hookInfraUnavailableReason as it stood before this
// story, restated so a drift in the constant is caught by the goldens.
const preChangeInfraReason = "satelle unavailable in this hook shell env — INFRASTRUCTURE failure, NOT a policy denial. " +
	"The satelle binary could not be resolved or did not produce a decision. " +
	"Try: which satelle; satelle version; satelle init. Non-mutating bash stays allowed so you can diagnose."

const (
	goldenClaudeDeny = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"R"}}`
	goldenGrokDeny   = `{"decision":"deny","reason":"R"}`
	goldenCursorDeny = `{"permission":"deny","user_message":"R","agent_message":"R"}`
)

func TestDenyPreToolUseCursor(t *testing.T) {
	prev := hookHarnessFlag
	hookHarnessFlag = "cursor"
	t.Cleanup(func() { hookHarnessFlag = prev })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	// A claude-shaped envelope: the flag, not the sniff, names the harness.
	raw := []byte(`{"tool_input":{"file_path":"/tmp/x.go"}}`)
	if err := denyPreToolUse(cmd, raw, "no engaged story"); err == nil {
		t.Fatal("denyPreToolUse must return an error so the verb exits non-zero")
	}
	want := `{"permission":"deny","user_message":"no engaged story","agent_message":"no engaged story"}` + "\n"
	if buf.String() != want {
		t.Fatalf("cursor deny = %q, want %q", buf.String(), want)
	}
}

// TestEmitPreToolUseDenyBytes pins every harness's deny bytes, and the infra
// deny bytes, to goldens captured before the encoding moved to agentcli.
func TestEmitPreToolUseDenyBytes(t *testing.T) {
	for harness, want := range map[string]string{
		"claude":  goldenClaudeDeny,
		"pi":      goldenClaudeDeny,
		"unknown": goldenClaudeDeny,
		"":        goldenClaudeDeny,
		"grok":    goldenGrokDeny,
		"cursor":  goldenCursorDeny,
	} {
		var buf bytes.Buffer
		if err := emitPreToolUseDeny(&buf, harness, "R"); err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		if buf.String() != want+"\n" {
			t.Errorf("emitPreToolUseDeny(%q) = %q, want %q", harness, buf.String(), want+"\n")
		}
	}

	claudeInfra := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"` + preChangeInfraReason + `"}}`
	grokInfra := `{"decision":"deny","reason":"` + preChangeInfraReason + `"}`
	cursorInfra := `{"permission":"deny","user_message":"` + preChangeInfraReason + `","agent_message":"` + preChangeInfraReason + `"}`
	for harness, want := range map[string]string{
		"claude": claudeInfra, "pi": claudeInfra, "grok": grokInfra, "cursor": cursorInfra,
	} {
		if got := infraDenyJSON(harness); got != want {
			t.Errorf("infraDenyJSON(%q) = %q, want %q", harness, got, want)
		}
	}
}

// strippedWrapperBody removes what this story adds to the generated wrapper —
// the cursor case arms and the cursor entry in the usage comment — so the rest
// can be compared with the body as it stood before.
func strippedWrapperBody(body string) string {
	var out []string
	for _, ln := range strings.Split(body, "\n") {
		trim := strings.TrimLeft(ln, " ")
		if strings.HasPrefix(trim, "cursor) ") {
			continue
		}
		if strings.HasPrefix(ln, "# args:") {
			ln = strings.Replace(ln, "claude|grok|pi|cursor", "claude|grok|pi", 1)
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func TestHookWrapperBodyMatchesPreChangeGolden(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "hook_wrapper_body.golden"))
	if err != nil {
		t.Fatal(err)
	}
	body := parameterizedHookScriptBody()
	if got := strippedWrapperBody(body); got != string(golden) {
		t.Fatalf("wrapper body drifted from the pre-change golden apart from the cursor arms:\n--- got\n%s\n--- want\n%s", got, golden)
	}
	if n := strings.Count(body, "cursor) "); n != 2 {
		t.Errorf("want exactly the infra and the deny-recognition cursor arm, got %d", n)
	}
	if !strings.Contains(body, `*'"permission"'*'"deny"'*`) {
		t.Errorf("cursor deny glob missing from the wrapper:\n%s", body)
	}
}

type wrapperRow struct {
	name   string
	sub    string
	event  func(harness string) string
	fake   string // "" = no satelle binary at all
	out    string
	code   int
	stdout func(harness string) string
}

// wrapperFakeSatelle writes a satelle stand-in into home that prints r.out
// verbatim to stdout and exits r.code. An empty r.fake installs no binary.
func wrapperFakeSatelle(t *testing.T, home string, r wrapperRow) {
	t.Helper()
	if r.fake == "" {
		return
	}
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(home, "payload.txt")
	if err := os.WriteFile(payload, []byte(r.out), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat " + payload + "\nexit " + string(rune('0'+r.code)) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "satelle"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestHookWrapperRows drives the generated wrapper for every wired harness. For
// claude, grok and pi the expected bytes are the pre-change literals; cursor's
// expectations come from agentcli.PreToolUseDeny.
func TestHookWrapperRows(t *testing.T) {
	repo := t.TempDir()
	if err := writeHookScripts(repo); err != nil {
		t.Fatal(err)
	}

	denyOf := func(harness string) string { return string(agentcli.PreToolUseDeny(harness, "policy R")) }
	infraOf := func(harness string) string { return string(agentcli.PreToolUseDeny(harness, preChangeInfraReason)) }
	edit := func(h string) string { return hookEditEvent(h) }
	bash := func(cmd string) func(string) string {
		return func(h string) string { return hookBashEvent(h, cmd) }
	}
	silent := func(string) string { return "" }
	line := func(f func(string) string) func(string) string {
		return func(h string) string { return f(h) + "\n" }
	}

	rows := []wrapperRow{
		{"gate deny exit1", "gate", edit, "y", "", 1, nil},
		{"gate deny exit0", "gate", edit, "y", "", 0, nil},
		{"gate empty output, exit 1", "gate", edit, "y", "", 1, line(infraOf)},
		{"gate malformed output, exit 0", "gate", edit, "y", "oops\n", 0, line(infraOf)},
		{"gate malformed output, exit 1", "gate", edit, "y", "oops\n", 1, line(infraOf)},
		{"gate no binary", "gate", edit, "", "", 0, line(infraOf)},
		{"gate allow", "gate", edit, "y", "", 0, silent},
		{"commitgate allow", "commitgate", bash("echo hello"), "y", "", 0, silent},
		{"commitgate non-mutating infra failure", "commitgate", bash("ls"), "y", "", 1, silent},
		{"commitgate non-mutating, no binary", "commitgate", bash("ls"), "", "", 0, silent},
		{"commitgate commit infra failure", "commitgate", bash("git commit -m x"), "y", "", 1, line(infraOf)},
		{"commitgate push infra failure", "commitgate", bash("git push"), "y", "oops\n", 0, line(infraOf)},
		{"commitgate commit, no binary", "commitgate", bash("git commit -m x"), "", "", 0, line(infraOf)},
	}

	for _, harness := range []string{"claude", "grok", "pi", "cursor"} {
		for _, r := range rows {
			t.Run(harness+"/"+r.name, func(t *testing.T) {
				r := r
				wantOut := r.stdout
				if strings.HasPrefix(r.name, "gate deny") {
					// The verb's own deny, passed through byte-for-byte.
					r.out = denyOf(harness) + "\n"
					wantOut = line(denyOf)
				}
				home := t.TempDir()
				wrapperFakeSatelle(t, home, r)
				code, stdout, stderr := runHookScript(t, repo,
					renderHookCommand(repo, harness, r.sub), r.event(harness), home)
				if code != 0 {
					t.Fatalf("handler exit = %d, want 0 (stdout=%q stderr=%q)", code, stdout, stderr)
				}
				if want := wantOut(harness); stdout != want {
					t.Fatalf("stdout = %q, want %q", stdout, want)
				}
			})
		}
	}

	// A commitgate deny the verb itself produced is passed through too.
	for _, harness := range []string{"claude", "grok", "pi", "cursor"} {
		home := t.TempDir()
		wrapperFakeSatelle(t, home, wrapperRow{fake: "y", out: denyOf(harness) + "\n", code: 1})
		_, stdout, _ := runHookScript(t, repo,
			renderHookCommand(repo, harness, "commitgate"), hookBashEvent(harness, "git commit -m x"), home)
		if stdout != denyOf(harness)+"\n" {
			t.Errorf("%s commitgate deny = %q", harness, stdout)
		}
	}

	// Cursor's infrastructure deny carries the reason where the model reads it.
	var doc map[string]any
	if err := json.Unmarshal([]byte(infraOf("cursor")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["permission"] != "deny" || doc["user_message"] != preChangeInfraReason {
		t.Errorf("cursor infra deny = %v", doc)
	}
}

// TestHookWrapperDenyGlobsDoNotOverlap: a harness's wrapper recognises only its
// own deny shape, so another harness's deny is not mistaken for a policy deny.
func TestHookWrapperDenyGlobsDoNotOverlap(t *testing.T) {
	repo := t.TempDir()
	if err := writeHookScripts(repo); err != nil {
		t.Fatal(err)
	}
	infra := func(h string) string { return string(agentcli.PreToolUseDeny(h, preChangeInfraReason)) + "\n" }
	for _, c := range []struct{ wrapper, verb string }{
		{"claude", "cursor"}, {"cursor", "claude"}, {"cursor", "grok"}, {"grok", "cursor"},
	} {
		home := t.TempDir()
		wrapperFakeSatelle(t, home, wrapperRow{fake: "y", out: string(agentcli.PreToolUseDeny(c.verb, "x")) + "\n", code: 1})
		_, stdout, _ := runHookScript(t, repo,
			renderHookCommand(repo, c.wrapper, "gate"), hookEditEvent(c.wrapper), home)
		if stdout != infra(c.wrapper) {
			t.Errorf("%s wrapper given a %s deny = %q, want its own infra deny", c.wrapper, c.verb, stdout)
		}
	}
}

func TestGateHelpNamesCursorDeny(t *testing.T) {
	gate, _, err := NewRootCmd().Find([]string{"hook", "gate"})
	if err != nil || gate == nil || gate.Name() != "gate" {
		t.Fatalf("hook gate command not found: %v", err)
	}
	long := gate.Long
	for _, want := range []string{
		"cursor", "Claude", "Grok", "internal/agentcli",
		"passes a deny it recognises", "infrastructure deny", "fails open for non-mutating shell",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("hook gate help does not mention %q", want)
		}
	}

	topic, ok := help.Get("agent-dispatch")
	if !ok {
		t.Fatal("agent-dispatch help topic missing")
	}
	row := regexp.MustCompile(`(?m)^\| Cursor \|.*$`).FindString(topic.Body)
	if row == "" {
		t.Fatal("agent-dispatch PreToolUse deny channels table has no Cursor row")
	}
	for _, want := range []string{"permission=deny", "user_message", "agent_message"} {
		if !strings.Contains(row, want) {
			t.Errorf("Cursor row does not state %q: %s", want, row)
		}
	}
}
