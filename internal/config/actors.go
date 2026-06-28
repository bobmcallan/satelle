package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ActorsConfigName is the per-repo actors-binding file, beside satelle.toml under
// the data dir (.satelle/actors.toml by default).
const ActorsConfigName = "actors.toml"

// Default actor grants — TODAY's behaviour, so an absent actors.toml changes
// nothing: the executor drives in-loop (the agent itself); the reviewer runs as
// an isolated agent with a READ-ONLY tool grant (see the
// satelle-recursive-actor-model principle — the reviewer is limited to reviewing).
const (
	DefaultExecutorHarness = "in-loop"
	DefaultReviewerHarness = "claude -p"
	DefaultReviewerTools   = "Read,Grep,Glob"
)

// ActorBinding binds one actor to a backend (how/where it runs) and its grant
// (the tool allowance, and an optional model). Empty fields take the defaults.
type ActorBinding struct {
	Harness string `toml:"harness"`
	Tools   string `toml:"tools"`
	Model   string `toml:"model"`
}

// ActorsConfig is the on-disk shape at .satelle/actors.toml — the actors layer.
// Every field is optional; the *Binding resolvers supply today's defaults, so
// the zero value (and an absent file) is the current behaviour.
type ActorsConfig struct {
	Executor ActorBinding `toml:"executor"`
	Reviewer ActorBinding `toml:"reviewer"`
}

// ReviewerBinding resolves the reviewer actor's backend and grant, defaulting to
// an isolated agent with the read-only tool grant. The grant travels with the
// binding, so the reviewer's read-only limit holds whatever the backend.
func (a ActorsConfig) ReviewerBinding() ActorBinding {
	b := a.Reviewer
	if b.Harness == "" {
		b.Harness = DefaultReviewerHarness
	}
	if b.Tools == "" {
		b.Tools = DefaultReviewerTools
	}
	return b
}

// ExecutorBinding resolves the executor actor's backend, defaulting to in-loop
// (the driving agent itself).
func (a ActorsConfig) ExecutorBinding() ActorBinding {
	b := a.Executor
	if b.Harness == "" {
		b.Harness = DefaultExecutorHarness
	}
	return b
}

// LoadActors reads <dataDir>/actors.toml (the actors layer). An absent file
// yields the zero ActorsConfig — defaults via the *Binding resolvers — and a nil
// error, so a repo with no actors.toml runs exactly as today.
func LoadActors(dataDir string) (ActorsConfig, error) {
	path := filepath.Join(dataDir, ActorsConfigName)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ActorsConfig{}, nil
		}
		return ActorsConfig{}, err
	}
	var ac ActorsConfig
	if _, err := toml.Decode(string(b), &ac); err != nil {
		return ActorsConfig{}, err
	}
	return ac, nil
}
