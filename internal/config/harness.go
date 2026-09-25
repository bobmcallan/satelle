// Per-harness SessionStart injection limit (sty_ce1a2733). Each in-loop harness
// delivers only so much hook additionalContext inline; the limit is a fact about
// that harness, so it is configuration — an embedded default
// (substrate/config/harness.toml) a repo overrides in satelle.toml — never a
// compiled constant tied to one provider.
package config

import (
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// HarnessConfig is one [harness.<name>] table.
//
//	[harness.claude]
//	context_limit_bytes = 9500
type HarnessConfig struct {
	// ContextLimitBytes is the SessionStart additionalContext budget for the
	// harness; zero or negative means unset (fall through to the default).
	ContextLimitBytes int `toml:"context_limit_bytes"`
}

const (
	harnessFile = "substrate/config/harness.toml"
	// unknownHarness names the fallback table for a harness with no entry of its
	// own. It is the adapter-neutral token, not any provider.
	unknownHarness = "unknown"
	// lastResortContextLimit applies only when neither the repo config nor the
	// embedded defaults yield a value (an unreadable embed). Neutral, and the
	// tightest safe budget.
	lastResortContextLimit = 8192
)

var (
	embeddedHarnessOnce sync.Once
	embeddedHarness     map[string]HarnessConfig
	embeddedHarnessErr  error
)

// EmbeddedHarness returns the embedded default [harness.*] tables. Parsed once;
// empty on parse failure (EmbeddedHarnessErr reports it, tests assert nil).
func EmbeddedHarness() map[string]HarnessConfig {
	embeddedHarnessOnce.Do(func() {
		raw, err := substrateFS.ReadFile(harnessFile)
		if err != nil {
			embeddedHarnessErr = err
			return
		}
		var file struct {
			Harness map[string]HarnessConfig `toml:"harness"`
		}
		if _, err := toml.Decode(string(raw), &file); err != nil {
			embeddedHarnessErr = err
			return
		}
		embeddedHarness = file.Harness
	})
	out := make(map[string]HarnessConfig, len(embeddedHarness))
	for k, v := range embeddedHarness {
		out[k] = v
	}
	return out
}

// EmbeddedHarnessErr is non-nil when the embedded harness defaults failed to
// load or parse. Exposed for tests.
func EmbeddedHarnessErr() error {
	_ = EmbeddedHarness()
	return embeddedHarnessErr
}

// ContextLimit returns the SessionStart injection budget (bytes) for harness.
// Resolution: the repo's [harness.<name>], the embedded [harness.<name>], the
// repo's [harness.unknown], the embedded [harness.unknown], then a neutral
// last-resort value. An unrecognised name never resolves to another provider.
func (c Config) ContextLimit(harness string) int {
	name := strings.ToLower(strings.TrimSpace(harness))
	if name == "" {
		name = unknownHarness
	}
	tables := []map[string]HarnessConfig{c.Harness, EmbeddedHarness()}
	for _, key := range []string{name, unknownHarness} {
		for _, tbl := range tables {
			if v := tbl[key].ContextLimitBytes; v > 0 {
				return v
			}
		}
	}
	return lastResortContextLimit
}

// MaxContextLimit returns the largest limit among the named harnesses (every
// configured one, plus the unknown fallback, when none are named) — the budget
// a resident set must fit for at least one harness to receive it inline.
func (c Config) MaxContextLimit(harnesses ...string) int {
	if len(harnesses) == 0 {
		harnesses = []string{unknownHarness}
		for k := range EmbeddedHarness() {
			harnesses = append(harnesses, k)
		}
		for k := range c.Harness {
			harnesses = append(harnesses, k)
		}
	}
	high := 0
	for _, h := range harnesses {
		if v := c.ContextLimit(h); v > high {
			high = v
		}
	}
	return high
}
