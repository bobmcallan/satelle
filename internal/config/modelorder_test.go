package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bobmcallan/satelle/internal/testutil"
)

const modelOrderDoc = `[model_order]
claude = ["opus", ["sonnet", "claude-sonnet-5"], "haiku"]
grok = ["grok-4.7"]
codex = ["gpt-5-codex"]

[executor]
command = "in-loop"
`

func decodeOrder(t *testing.T, doc string) AgentsConfig {
	t.Helper()
	ac, err := decodeAgents(doc, false)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return ac
}

// TestSelectModel_BindingPinBeatsOrder: AC1 — a binding's own model= wins over
// the order, and a step/agent override does too.
func TestSelectModel_BindingPinBeatsOrder(t *testing.T) {
	in := SelectInput{
		Binding: "sonnet", Order: []ModelRank{{"opus"}},
		HasModelSlot: true, CommandExecutable: "claude",
	}
	if m, src := SelectModel(in); m != "sonnet" || src != ModelSourceBinding {
		t.Errorf("got (%q, %q), want (sonnet, binding)", m, src)
	}
	in.Binding, in.DispatchOverride, in.DispatchSource = "", "haiku", ModelSourceStep
	if m, src := SelectModel(in); m != "haiku" || src != ModelSourceStep {
		t.Errorf("got (%q, %q), want (haiku, step)", m, src)
	}
}

// TestSelectModel_OrderFirstEntry: AC2 — an empty binding with no inherited
// session model takes the first entry of its own executable's order.
func TestSelectModel_OrderFirstEntry(t *testing.T) {
	ac := decodeOrder(t, modelOrderDoc)
	for _, tc := range []struct{ exe, want string }{
		{"claude", "opus"}, {"grok", "grok-4.7"}, {"codex", "gpt-5-codex"},
	} {
		in := SelectInput{Order: ac.OrderFor(tc.exe), HasModelSlot: true, CommandExecutable: tc.exe}
		if m, src := SelectModel(in); m != tc.want || src != ModelSourceOrder {
			t.Errorf("%s: got (%q, %q), want (%q, order)", tc.exe, m, src, tc.want)
		}
	}
	// ACP applies a model without a {model} slot.
	in := SelectInput{Order: ac.OrderFor("grok"), ModelViaSession: true, CommandExecutable: "grok"}
	if m, src := SelectModel(in); m != "grok-4.7" || src != ModelSourceOrder {
		t.Errorf("acp: got (%q, %q)", m, src)
	}
	// A same-executable inherited model beats the order.
	in = SelectInput{
		Order: ac.OrderFor("claude"), HasModelSlot: true, CommandExecutable: "claude",
		Orchestrator: SessionModel{Model: "claude-sonnet-5", Executable: "claude"},
	}
	if m, src := SelectModel(in); m != "claude-sonnet-5" || src != ModelSourceInheritedOrchestrator {
		t.Errorf("inherited: got (%q, %q)", m, src)
	}
	// A rank group applies its first name.
	if m, _ := SelectModel(SelectInput{Order: []ModelRank{{"opus", "claude-opus-5-5"}}, HasModelSlot: true, CommandExecutable: "claude"}); m != "opus" {
		t.Errorf("rank group applied %q, want opus", m)
	}
	// No slot and no session config: nothing can apply the order.
	if m, src := SelectModel(SelectInput{Order: ac.OrderFor("claude"), CommandExecutable: "claude"}); m != "" || src != ModelSourceCLIDefault {
		t.Errorf("no slot: got (%q, %q)", m, src)
	}
}

// TestSelectModel_OrderNeverCrossesExecutable: AC3 — only the dispatch
// executable's own list is consulted, in both directions.
func TestSelectModel_OrderNeverCrossesExecutable(t *testing.T) {
	claudeOnly := decodeOrder(t, "[model_order]\nclaude = [\"opus\"]\n")
	for _, exe := range []string{"grok", "codex", "unknown-cli", ""} {
		in := SelectInput{Order: claudeOnly.OrderFor(exe), HasModelSlot: true, CommandExecutable: exe}
		if m, src := SelectModel(in); m != "" || src != ModelSourceCLIDefault {
			t.Errorf("claude list reached %q: got (%q, %q)", exe, m, src)
		}
	}
	others := decodeOrder(t, "[model_order]\ngrok = [\"grok-4.7\"]\ncodex = [\"gpt-5-codex\"]\n")
	in := SelectInput{Order: others.OrderFor("claude"), HasModelSlot: true, CommandExecutable: "claude"}
	if m, src := SelectModel(in); m != "" || src != ModelSourceCLIDefault {
		t.Errorf("grok/codex list reached claude: got (%q, %q)", m, src)
	}
}

func TestOrderFor(t *testing.T) {
	ac := decodeOrder(t, modelOrderDoc)
	if got := ac.OrderFor("Grok"); len(got) != 1 || got[0].First() != "grok-4.7" {
		t.Errorf("OrderFor(Grok) = %v", got)
	}
	if reflect.DeepEqual(ac.OrderFor("grok"), ac.OrderFor("claude")) {
		t.Error("grok and claude share a list")
	}
	if ac.OrderFor("gemini") != nil || ac.OrderFor("") != nil {
		t.Error("unknown executable must get nil, not a Claude default")
	}
	if _, ok := ac.Agents["model_order"]; ok {
		t.Error("[model_order] was read as a binding")
	}
}

func TestEncodeAgents_ModelOrderRoundTrip(t *testing.T) {
	ac := decodeOrder(t, modelOrderDoc)
	b1, err := EncodeAgents(ac)
	if err != nil {
		t.Fatal(err)
	}
	ac2 := decodeOrder(t, string(b1))
	if !reflect.DeepEqual(ac.ModelOrder, ac2.ModelOrder) {
		t.Errorf("round trip lost order: %v vs %v", ac.ModelOrder, ac2.ModelOrder)
	}
	if got := ac2.OrderFor("claude"); len(got) != 3 || len(got[1]) != 2 {
		t.Errorf("rank group lost: %v", got)
	}
	b2, _ := EncodeAgents(ac2)
	if !bytes.Equal(b1, b2) {
		t.Errorf("encode not deterministic:\n%s\n---\n%s", b1, b2)
	}
}

func TestEncodeAgents_ModelOrderNotABinding(t *testing.T) {
	ac := AgentsConfig{Agents: map[string]AgentBinding{"model_order": {Command: "x"}}}
	out, err := EncodeAgents(ac)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("model_order")) {
		t.Errorf("binding named model_order was emitted: %s", out)
	}
}

func TestRedactAgentsTransport_KeepsModelOrder(t *testing.T) {
	out, err := RedactAgentsTransport([]byte(modelOrderDoc))
	if err != nil {
		t.Fatal(err)
	}
	ac := decodeOrder(t, string(out))
	for _, exe := range []string{"claude", "grok", "codex"} {
		if len(ac.OrderFor(exe)) == 0 {
			t.Errorf("redacted transport lost the %s list", exe)
		}
	}
}

func TestRehydrateAgents_KeepsModelOrder(t *testing.T) {
	store := "[model_order]\ngrok = [\"grok-4.7\"]\n\n[executor]\ncommand = \"in-loop\"\n"
	local := "[model_order]\ngrok = [\"local-only\"]\n\n[executor]\ncommand = \"in-loop\"\n\n[extra]\ncommand = \"x\"\n"
	out, _, err := RehydrateAgents([]byte(store), []byte(local))
	if err != nil {
		t.Fatal(err)
	}
	ac := decodeOrder(t, string(out))
	if got := ac.OrderFor("grok"); len(got) != 1 || got[0].First() != "grok-4.7" {
		t.Errorf("rehydrate dropped the store order: %v", got)
	}
}

func TestResolveAgentsBaseline_ModelOrderLayers(t *testing.T) {
	baseline := AgentsConfig{ModelOrder: map[string][]ModelRank{"codex": {{"gpt-5-codex"}}, "claude": {{"haiku"}}}}
	workspace := AgentsConfig{ModelOrder: map[string][]ModelRank{"grok": {{"grok-4.7"}}, "claude": {{"sonnet"}}}}
	repo := AgentsConfig{ModelOrder: map[string][]ModelRank{"claude": {{"opus"}}}}
	out, _, err := ResolveAgentsBaseline(baseline, repo, workspace, GlobalAgentsConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for exe, want := range map[string]string{"claude": "opus", "grok": "grok-4.7", "codex": "gpt-5-codex"} {
		got := out.OrderFor(exe)
		if len(got) == 0 || got[0].First() != want {
			t.Errorf("%s: got %v, want first %q", exe, got, want)
		}
	}
}

func TestLoadEffectiveAgents_ModelOrder(t *testing.T) {
	testutil.IsolateHome(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, AgentsConfigDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AgentsConfigDir, AgentsConfigName), []byte(modelOrderDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	eff, err := LoadEffectiveAgents(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, exe := range []string{"claude", "grok", "codex"} {
		if len(eff.Agents.OrderFor(exe)) == 0 {
			t.Errorf("effective layer lost the %s list", exe)
		}
	}
}
