package config

import "testing"

// TestModelsRankingLoadedFromAgentsToml pins AC2 at the DECODE boundary, not
// just the pure SelectModel function: two agents.toml bodies differing only
// in [models] ranking feed a SelectInput identically, and the inherited pick
// flips with no code change — proving the table actually round-trips from
// the file an operator edits into what SelectModel reads.
func TestModelsRankingLoadedFromAgentsToml(t *testing.T) {
	body := func(ranking string) string {
		return "[models]\nranking = " + ranking + "\n\n[executor]\ncommand = \"in-loop\"\n"
	}
	in := SelectInput{
		HasModelSlot: true, CommandExecutable: "claude",
		Orchestrator: SessionModel{Model: "haiku", Executable: "claude"},
		InLoop:       SessionModel{Model: "sonnet", Executable: "claude"},
	}

	ac, err := decodeAgents(body(`["haiku", "sonnet", "opus"]`), false)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in.Ranking = ac.Models.Ranking
	if model, source := SelectModel(in); model != "haiku" || source != ModelSourceInheritedOrchestrator {
		t.Fatalf("ranking [haiku,sonnet,opus] via agents.toml: got (%q, %q)", model, source)
	}

	ac, err = decodeAgents(body(`["sonnet", "haiku", "opus"]`), false)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	in.Ranking = ac.Models.Ranking
	if model, source := SelectModel(in); model != "sonnet" || source != ModelSourceInheritedInLoop {
		t.Fatalf("ranking [sonnet,haiku,opus] via agents.toml: got (%q, %q)", model, source)
	}
}

// defaultRanking mirrors this repo's own [models] ranking.
var defaultRanking = []string{"opus", "sonnet", "haiku"}

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
				Ranking:      defaultRanking,
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
				Ranking:      defaultRanking,
			},
			wantModel:  "sonnet",
			wantSource: ModelSourceInheritedInLoop,
		},
		{
			name: "inherited picks higher-ranked of the two sessions",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "haiku", Executable: "claude"},
				InLoop:       SessionModel{Model: "opus", Executable: "claude"},
				Ranking:      defaultRanking,
			},
			wantModel:  "opus",
			wantSource: ModelSourceInheritedInLoop,
		},
		{
			name: "inherited tie goes to orchestrator",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "opus", Executable: "claude"},
				InLoop:       SessionModel{Model: "opus", Executable: "claude"},
				Ranking:      defaultRanking,
			},
			wantModel:  "opus",
			wantSource: ModelSourceInheritedOrchestrator,
		},
		{
			name: "creator when orchestrator and in-loop both unknown",
			in: SelectInput{
				HasModelSlot: true, CommandExecutable: "claude",
				Orchestrator: SessionModel{Model: "unknown", Executable: "claude"},
				InLoop:       SessionModel{Model: "", Executable: "claude"},
				Creator:      SessionModel{Model: "sonnet", Executable: "claude"},
				Ranking:      defaultRanking,
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

// TestSelectModelRankingFromConfig pins AC2: changing the ranking flips the
// inherited pick with no code change.
func TestSelectModelRankingFromConfig(t *testing.T) {
	in := SelectInput{
		HasModelSlot: true, CommandExecutable: "claude",
		Orchestrator: SessionModel{Model: "haiku", Executable: "claude"},
		InLoop:       SessionModel{Model: "sonnet", Executable: "claude"},
	}

	in.Ranking = []string{"haiku", "sonnet", "opus"}
	model, source := SelectModel(in)
	if model != "haiku" || source != ModelSourceInheritedOrchestrator {
		t.Fatalf("ranking [haiku,sonnet,opus]: got (%q, %q)", model, source)
	}

	in.Ranking = []string{"sonnet", "haiku", "opus"}
	model, source = SelectModel(in)
	if model != "sonnet" || source != ModelSourceInheritedInLoop {
		t.Fatalf("ranking [sonnet,haiku,opus]: got (%q, %q)", model, source)
	}
}

// TestUnrankedLosesToRanked pins AC2's explicit clause.
func TestUnrankedLosesToRanked(t *testing.T) {
	in := SelectInput{
		HasModelSlot: true, CommandExecutable: "claude",
		Orchestrator: SessionModel{Model: "glm-4.6", Executable: "claude"}, // unranked
		InLoop:       SessionModel{Model: "haiku", Executable: "claude"},   // ranked, weakest
		Ranking:      defaultRanking,
	}
	model, source := SelectModel(in)
	if model != "haiku" || source != ModelSourceInheritedInLoop {
		t.Fatalf("unranked-vs-ranked: got (%q, %q), want (haiku, inherited-in-loop)", model, source)
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
		Ranking: defaultRanking,
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
