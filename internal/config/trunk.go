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

	// BaseRefuse lists the states that stop a worktree cut from the trunk and
	// `satelle trunk sync --strict` (sty_92337a13), which the container's merge
	// step runs. Absent means DefaultTrunkBaseRefuse; an explicit empty list
	// stops nothing. "unresolved" (no trunk could be named) and "behind" (behind
	// and not fast-forwarded) are states of this list.
	BaseRefuse *[]string `toml:"base_refuse"`
	// Branch is the trunk's name when the remote's HEAD ref does not give one:
	// the hint `story worktree --trunk-branch` and `trunk sync --trunk-branch`
	// override.
	Branch string `toml:"branch"`
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

// DefaultTrunkBaseRefuse is what an absent [trunk] base_refuse means. It is
// wider than DefaultTrunkRefuse on purpose. An engage only reads the trunk, so
// an offline remote proceeds with a warning and an unpushed trunk is a notice.
// A cut or a merge writes new history from its base, so a base that is not shown
// to be level with the remote (ahead, offline, unresolved, or behind and not
// moved) is a stop.
var DefaultTrunkBaseRefuse = []string{"dirty", "diverged", "ahead", "behind", "offline", "unresolved"}

// DefaultTrunkRefuse is what an absent [trunk] refuse means: a dirty or a
// diverged trunk would only surface later as a rejected push, so work does not
// start on it. Behind is fast-forwarded, ahead is only a notice, and offline
// proceeds with a warning.
var DefaultTrunkRefuse = []string{"dirty", "diverged"}

// trunkRefusable are the states a [trunk] refuse list may name. Level and
// skipped have nothing to refuse.
// "unresolved" only has an effect in base_refuse: an engage skips a trunk it
// cannot name.
var trunkRefusable = []string{"dirty", "diverged", "behind", "ahead", "offline", "unresolved"}

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

// BaseRefuseSet is the declared base_refuse list, or DefaultTrunkBaseRefuse when
// absent.
func (t TrunkConfig) BaseRefuseSet() []string {
	if t.BaseRefuse == nil {
		return append([]string(nil), DefaultTrunkBaseRefuse...)
	}
	return append([]string(nil), *t.BaseRefuse...)
}

// BaseRefuses reports whether state is in the base_refuse set.
func (t TrunkConfig) BaseRefuses(state string) bool {
	for _, s := range t.BaseRefuseSet() {
		if s == state {
			return true
		}
	}
	return false
}

// ValidateTrunkStates refuses a state no trunk report can have. It serves the
// config keys and the --refuse flag alike.
func ValidateTrunkStates(states []string) error {
	for _, s := range states {
		ok := false
		for _, known := range trunkRefusable {
			ok = ok || s == known
		}
		if !ok {
			return fmt.Errorf("names %q (want any of %v)", s, trunkRefusable)
		}
	}
	return nil
}

// validateTrunk refuses an unknown state in [trunk] refuse or base_refuse, and a
// publish_rounds below 1, at load time, so a typo cannot silently disarm a check.
func validateTrunk(cfg Config, path string) error {
	if cfg.Trunk.PublishRounds != nil && *cfg.Trunk.PublishRounds < 1 {
		return fmt.Errorf("config %s: [trunk] publish_rounds is %d (want 1 or more)", path, *cfg.Trunk.PublishRounds)
	}
	for key, list := range map[string]*[]string{"refuse": cfg.Trunk.Refuse, "base_refuse": cfg.Trunk.BaseRefuse} {
		if list == nil {
			continue
		}
		if err := ValidateTrunkStates(*list); err != nil {
			return fmt.Errorf("config %s: [trunk] %s %w", path, key, err)
		}
	}
	return nil
}
