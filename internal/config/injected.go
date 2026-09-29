package config

import "strings"

// InstructionReviewConfig is the [instruction_review] table:
//
//	[instruction_review]
//	extra_paths = ["internal/config/substrate/skills/**"]
//
// The shipped trigger skill (satelle-instruction-change-trigger) counts changes
// to .satelle/{skills,principles} and the constitution; a repo that keeps
// injected instruction text elsewhere — embedded defaults in its own tree — names
// the globs here. The trigger's shell script reads the key from the file, so no
// Go code consumes it; the field documents the schema and keeps the key a known
// one. The binary ships no path of its own.
type InstructionReviewConfig struct {
	ExtraPaths []string `toml:"extra_paths"`
}

// DefaultBytesPerToken is the conversion ratio the injected-size report divides
// bytes by. It is a unit conversion, not a budget: no adapter reports tokens
// before a call, so one provider-neutral estimate serves every seat.
const DefaultBytesPerToken = 4

// ValidateConfig is the [validate] table.
type ValidateConfig struct {
	Injected InjectedConfig `toml:"injected"`
}

// InjectedConfig is the [validate.injected] table:
//
//	[validate.injected]
//	bytes_per_token = 4
//
//	[validate.injected.budget]
//	driver   = 6000    # estimated tokens
//	reviewer = 9000
//
// Budget keys are a seat name (driver, coder, reviewer, …) or a kind name
// (principles, constitution, skill), or an exact row label such as
// driver[claude]; the values are estimated tokens. The binary ships no budget:
// a key absent from this table means "no budget" for that row, and an exceeded
// budget only warns.
type InjectedConfig struct {
	BytesPerToken int            `toml:"bytes_per_token"`
	Budget        map[string]int `toml:"budget"`
}

// ResolveBytesPerToken returns the configured ratio, or DefaultBytesPerToken when
// unset or not positive.
func (c InjectedConfig) ResolveBytesPerToken() int {
	if c.BytesPerToken > 0 {
		return c.BytesPerToken
	}
	return DefaultBytesPerToken
}

// BudgetFor returns the configured token budget for a report row: the exact row
// label first (driver[claude]), then its base name (driver). ok is false when the
// repo set none — there is no default.
func (c InjectedConfig) BudgetFor(label string) (tokens int, ok bool) {
	if v, ok := c.Budget[label]; ok && v > 0 {
		return v, true
	}
	base, _, _ := strings.Cut(label, "[")
	if v, ok := c.Budget[base]; ok && v > 0 {
		return v, true
	}
	return 0, false
}
