package config

import "fmt"

// TrunkConfig is the [trunk] table (sty_9f3e51d1): what the start-of-work trunk
// check does when a story or an epic is engaged. The check itself is mechanism
// (internal/trunk); which states stop an engage is this repo's decision, so it
// is declared here and not compiled into a verb.
//
// Both keys are pointers so an absent key is told apart from an explicit
// false or an explicit empty list.
type TrunkConfig struct {
	// Check turns the engage-time check on or off. Absent means on.
	Check *bool `toml:"check"`
	// Refuse lists the trunk states (dirty, diverged, behind, ahead, offline)
	// that refuse the engage after the report is printed. Absent means
	// DefaultTrunkRefuse; an explicit empty list refuses nothing. A behind
	// trunk that the check fast-forwarded is resolved, so "behind" refuses
	// only when the trunk could not be moved.
	Refuse *[]string `toml:"refuse"`

	// Prove is the shell command `satelle trunk publish` runs on the combined
	// head before it pushes (sty_6af229f1). There is no default: what proves a
	// repo is its own decision. Absent, publish refuses.
	Prove string `toml:"prove"`
	// Stamp is an optional shell command publish runs on the integrated tree
	// before Prove: it computes the release's version bump and changelog entry
	// from that tree and commits them.
	Stamp string `toml:"stamp"`
	// PublishRounds bounds how many times a refused push is answered by
	// integrating the moved trunk again. Absent means DefaultPublishRounds.
	PublishRounds *int `toml:"publish_rounds"`
}

// DefaultPublishRounds is what an absent [trunk] publish_rounds means.
const DefaultPublishRounds = 5

// PublishRoundBound is the declared round bound, or DefaultPublishRounds.
func (t TrunkConfig) PublishRoundBound() int {
	if t.PublishRounds == nil {
		return DefaultPublishRounds
	}
	return *t.PublishRounds
}

// DefaultTrunkRefuse is what an absent [trunk] refuse means: a dirty or a
// diverged trunk would only surface later as a rejected push, so work does not
// start on it. Behind is fast-forwarded, ahead is only a notice, and offline
// proceeds with a warning.
var DefaultTrunkRefuse = []string{"dirty", "diverged"}

// trunkRefusable are the states a [trunk] refuse list may name. Level and
// skipped have nothing to refuse.
var trunkRefusable = []string{"dirty", "diverged", "behind", "ahead", "offline"}

// Enabled reports whether the engage-time trunk check runs.
func (t TrunkConfig) Enabled() bool { return t.Check == nil || *t.Check }

// RefuseSet is the declared refuse list, or DefaultTrunkRefuse when absent.
func (t TrunkConfig) RefuseSet() []string {
	if t.Refuse == nil {
		return append([]string(nil), DefaultTrunkRefuse...)
	}
	return append([]string(nil), *t.Refuse...)
}

// Refuses reports whether state is in the refuse set.
func (t TrunkConfig) Refuses(state string) bool {
	for _, s := range t.RefuseSet() {
		if s == state {
			return true
		}
	}
	return false
}

// validateTrunk refuses an unknown state in [trunk] refuse at load time, so a
// typo cannot silently disarm the check.
func validateTrunk(cfg Config, path string) error {
	if cfg.Trunk.PublishRounds != nil && *cfg.Trunk.PublishRounds < 1 {
		return fmt.Errorf("config %s: [trunk] publish_rounds is %d (want 1 or more)", path, *cfg.Trunk.PublishRounds)
	}
	if cfg.Trunk.Refuse == nil {
		return nil
	}
	for _, s := range *cfg.Trunk.Refuse {
		ok := false
		for _, known := range trunkRefusable {
			ok = ok || s == known
		}
		if !ok {
			return fmt.Errorf("config %s: [trunk] refuse names %q (want any of %v)", path, s, trunkRefusable)
		}
	}
	return nil
}
