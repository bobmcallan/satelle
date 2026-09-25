package agentcli

import (
	"encoding/json"
	"strings"
	"testing"
)

// Resolved model per adapter/transport (sty_8e422d47): a measured id when the
// provider exposes one, else an adapter-named reason — never the bare literal
// and never empty.
func TestModelResolved_MeasuredPerAdapter(t *testing.T) {
	cases := []struct {
		name string
		got  func() string
		want string
	}{
		{"grok command json", func() string {
			_, u := UnwrapUsage(usageFixture(t, "grok_json.json"))
			return u.ModelResolved
		}, "grok-4.5-build"},
		{"grok stream", func() string {
			_, u := UnwrapUsage(usageFixture(t, "grok_streaming.jsonl"))
			return u.ModelResolved
		}, "grok-4.5-build"},
		{"grok acp", func() string {
			u := acpUsageFromResult(acpFixtureResult(t), "", "grok acp")
			return u.ModelResolved
		}, "grok-4.7"},
		{"grok acp modelUsage fallback", func() string {
			u := acpUsageFromResult(json.RawMessage(`{"_meta":{"usage":{"inputTokens":1,"outputTokens":1,"modelUsage":{"grok-4.7-build":{"outputTokens":1}}}}}`), "", "grok acp")
			return u.ModelResolved
		}, "grok-4.7-build"},
		{"acp session reply fallback", func() string {
			u := acpUsageFromResult(json.RawMessage(`{"stopReason":"end_turn"}`), acpModelFromReply(json.RawMessage(`{"sessionId":"s","models":{"currentModelId":"m-1"}}`)), "codex acp")
			return u.ModelResolved
		}, "m-1"},
		{"codex command with model", func() string {
			_, u := UnwrapUsage(usageFixture(t, "codex_exec_model.jsonl"))
			return u.ModelResolved
		}, "gpt-5-codex"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.got(); got != c.want {
				t.Errorf("ModelResolved = %q, want %q", got, c.want)
			}
		})
	}
}

func TestModelResolved_AdapterNamedReasonPerAdapter(t *testing.T) {
	const claudeNoModel = `{"result":"ok","usage":{"input_tokens":1,"output_tokens":1}}`
	cases := []struct {
		name string
		got  func() string
		want string
	}{
		{"claude command", func() string {
			_, u := UnwrapUsage([]byte(claudeNoModel))
			return u.ModelResolved
		}, "unavailable: claude command reports no model"},
		{"claude stream", func() string {
			_, u := UnwrapUsage([]byte(`{"type":"result","usage":{"input_tokens":3,"output_tokens":2}}` + "\n" + `{"type":"x"}`))
			return u.ModelResolved
		}, "unavailable: claude stream reports no model"},
		{"grok command no usage", func() string {
			_, u := UnwrapUsage(usageFixture(t, "grok_json_nousage.json"))
			return u.ModelResolved
		}, "unavailable: grok command reports no model"},
		{"grok command usage without modelUsage", func() string {
			_, u := UnwrapUsage([]byte(`{"text":"ok","usage":{"input_tokens":3,"output_tokens":2}}`))
			return u.ModelResolved
		}, "unavailable: grok command reports no model"},
		{"grok stream", func() string {
			_, u := UnwrapUsage([]byte(`{"type":"usage","usage":{"input_tokens":3,"output_tokens":2}}` + "\n" + `{"type":"end","usage":{"input_tokens":3,"output_tokens":2}}`))
			return u.ModelResolved
		}, "unavailable: grok stream reports no model"},
		{"grok acp", func() string {
			return acpUsageFromResult(json.RawMessage(`{"_meta":{"usage":{"inputTokens":1,"outputTokens":1}}}`), "", "grok acp").ModelResolved
		}, "unavailable: grok acp reports no model"},
		{"codex acp", func() string {
			return acpUsageFromResult(json.RawMessage(`{"_meta":{"usage":{"inputTokens":1,"outputTokens":1}}}`), "", "codex acp").ModelResolved
		}, "unavailable: codex acp reports no model"},
		{"codex command", func() string {
			_, u := UnwrapUsage(usageFixture(t, "codex_exec.jsonl"))
			return u.ModelResolved
		}, "unavailable: codex command reports no model"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.got()
			if got != c.want {
				t.Errorf("ModelResolved = %q, want %q", got, c.want)
			}
			if !IsModelUnavailable(got) {
				t.Errorf("IsModelUnavailable(%q) = false", got)
			}
		})
	}
}

func TestACPAdapterLabel(t *testing.T) {
	cases := []struct {
		binary string
		args   []string
		want   string
	}{
		{"grok", []string{"agent", "stdio"}, "grok acp"},
		{"/usr/bin/grok", []string{"agent", "stdio"}, "grok acp"},
		{"npx", []string{"-y", "@agentclientprotocol/codex-acp"}, "codex acp"},
		{"codex-acp", []string{"stdio"}, "codex acp"},
		{"/tmp/fake-peer", []string{"stdio"}, "acp fake-peer"},
	}
	for _, c := range cases {
		if got := acpAdapterLabel(c.binary, c.args); got != c.want {
			t.Errorf("acpAdapterLabel(%q, %v) = %q, want %q", c.binary, c.args, got, c.want)
		}
	}
}

// An id seen on one codex turn survives the per-turn sum; a later reason never
// replaces a measured id.
func TestAddUsage_KeepsMeasuredModel(t *testing.T) {
	a := UsageResult{ModelResolved: "gpt-5-codex", Available: true}
	b := UsageResult{ModelResolved: noModelReport("codex command"), Available: true}
	if got := addUsage(a, b).ModelResolved; got != "gpt-5-codex" {
		t.Errorf("model = %q, want gpt-5-codex kept", got)
	}
	if got := addUsage(b, a).ModelResolved; got != "gpt-5-codex" {
		t.Errorf("model = %q, want a later measured id to replace a reason", got)
	}
}

func TestIsModelUnavailable(t *testing.T) {
	for _, s := range []string{"", ModelUnavailable, ModelUnavailableFor("x", "reports no model")} {
		if !IsModelUnavailable(s) {
			t.Errorf("IsModelUnavailable(%q) = false", s)
		}
	}
	if IsModelUnavailable("grok-4.7") {
		t.Error("a measured id is not unavailable")
	}
	if !strings.HasPrefix(ModelUnavailableFor("codex command", "reports no model"), "unavailable: codex command") {
		t.Error("reason must name the adapter")
	}
}
