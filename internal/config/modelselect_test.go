package config

import "testing"

// TestModelsSectionIgnored: the retired [models] ranking table still loads in
// an older repo's agents.toml, is not read as an agent binding, and feeds
// nothing.
func TestModelsSectionIgnored(t *testing.T) {
	ac, err := decodeAgents("[models]\nranking = [\"opus\", \"sonnet\", \"haiku\"]\n\n[executor]\ncommand = \"in-loop\"\n", false)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := ac.Agents["models"]; ok {
		t.Errorf("[models] was read as an agent binding: %+v", ac.Agents)
	}
	if ac.Executor.Command != "in-loop" {
		t.Errorf("executor lost: %+v", ac.Executor)
	}
}

// TestSelectModelPrecedence pins AC1: first match wins across every tier, and
// each level falls through cleanly when unset/unknown/guard-failed.
func TestSelectModelPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		in         SelectInput
		wantModel  string
		wantSource string
	}{
		{
			name:       "binding wins outright",
			in:         SelectInput{Binding: "opus", DispatchOverride: "sonnet", DispatchSource: ModelSourceAgent},
			wantModel:  "opus",
			wantSource: ModelSourceBinding,
		},
		{
			name: "step override when binding empty",
			in: SelectInput{
				DispatchOverride: "sonnet", DispatchSource: ModelSourceStep,
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "opus", Executable: "claude"},
			},
			wantModel:  "sonnet",
			wantSource: ModelSourceStep,
		},
		{
			name: "agent flag override when binding empty",
			in: SelectInput{
				DispatchOverride: "haiku", DispatchSource: ModelSourceAgent,
			},
			wantModel:  "haiku",
			wantSource: ModelSourceAgent,
		},
		{
			name:       "unlabeled dispatch override defaults to agent source",
			in:         SelectInput{DispatchOverride: "haiku"},
			wantModel:  "haiku",
			wantSource: ModelSourceAgent,
		},
		{
			name: "inherited orchestrator when no binding/override",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "opus", Executable: "claude"},
				InLoop:       SessionModel{Model: "unknown", Executable: "claude"},
			},
			wantModel:  "opus",
			wantSource: ModelSourceInheritedOrchestrator,
		},
		{
			name: "inherited in-loop when orchestrator unknown",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "unknown", Executable: "claude"},
				InLoop:       SessionModel{Model: "sonnet", Executable: "claude"},
			},
			wantModel:  "sonnet",
			wantSource: ModelSourceInheritedInLoop,
		},
		{
			name: "orchestrator wins when both sessions are eligible, whatever the model",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "haiku", Executable: "claude"},
				InLoop:       SessionModel{Model: "opus", Executable: "claude"},
			},
			wantModel:  "haiku",
			wantSource: ModelSourceInheritedOrchestrator,
		},
		{
			name: "creator when orchestrator and in-loop both unknown",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "unknown", Executable: "claude"},
				InLoop:       SessionModel{Model: "", Executable: "claude"},
				Creator:      SessionModel{Model: "sonnet", Executable: "claude"},
			},
			wantModel:  "sonnet",
			wantSource: ModelSourceCreator,
		},
		{
			name:       "cli-default when nothing resolves",
			in:         SelectInput{},
			wantModel:  "",
			wantSource: ModelSourceCLIDefault,
		},
		{
			name: "cli-default when no model slot even with known sessions",
			in: SelectInput{
				HasModelSlot: false, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "opus", Executable: "claude"},
				Creator:      SessionModel{Model: "opus", Executable: "claude"},
			},
			wantModel:  "",
			wantSource: ModelSourceCLIDefault,
		},
		{
			name: "cross-provider guard blocks a mismatched executable, falls to creator",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "codex",
				Orchestrator: SessionModel{Model: "claude-opus-5-5", Executable: "claude"},
				Creator:      SessionModel{Model: "gpt-5-codex", Executable: "codex"},
			},
			wantModel:  "gpt-5-codex",
			wantSource: ModelSourceCreator,
		},
		{
			name: "cross-provider guard blocks creator too, falls to cli-default",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "codex",
				Creator: SessionModel{Model: "claude-opus-5-5", Executable: "claude"},
			},
			wantModel:  "",
			wantSource: ModelSourceCLIDefault,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model, source := SelectModel(tc.in)
			if model != tc.wantModel || source != tc.wantSource {
				t.Errorf("SelectModel() = (%q, %q), want (%q, %q)", model, source, tc.wantModel, tc.wantSource)
			}
		})
	}
}

// TestSelectModelExplicitBindingUnchanged pins AC6: an explicit binding model
// behaves exactly as before, regardless of every other tier.
func TestSelectModelExplicitBindingUnchanged(t *testing.T) {
	in := SelectInput{
		Binding:          "opus",
		DispatchOverride: "sonnet", DispatchSource: ModelSourceStep,
		Orchestrator: SessionModel{Model: "haiku", Executable: "claude"},
		Creator:      SessionModel{Model: "haiku", Executable: "claude"},
		HasModelSlot: true, CommandExecutable: "claude",
	}
	model, source := SelectModel(in)
	if model != "opus" || source != ModelSourceBinding {
		t.Fatalf("explicit binding must win outright: got (%q, %q)", model, source)
	}
}

func TestHasModelSlot(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"claude -p --model {model}", true},
		{`codex exec -s read-only -m {model} -c model_reasoning_effort="{effort}"`, true},
		{"claude -p --output-format json", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := HasModelSlot(tc.command); got != tc.want {
			t.Errorf("HasModelSlot(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
}
