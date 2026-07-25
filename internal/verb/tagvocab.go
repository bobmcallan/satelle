package verb

import (
	"github.com/bobmcallan/satelle/internal/config"
)

// tag vocabulary wiring (sty_034d843c): config-declared controlled tag
// namespaces reach create/set via a package global, same shape as
// SetAgentsConfig. Unwired (tests / pre-init) = no-op so every existing verb
// test keeps passing without opting in.

var (
	tagVocabWired bool
	tagVocabCfg   config.Config
)

// SetTagVocabulary wires the repo's tag vocabulary for create/set validation.
// Call with a.Config at CLI bootstrap. Pass zero / omit in tests that do not
// need the check.
func SetTagVocabulary(cfg config.Config) {
	tagVocabCfg = cfg
	tagVocabWired = true
}

// ClearTagVocabulary resets the tag vocabulary wiring (tests).
func ClearTagVocabulary() {
	tagVocabCfg = config.Config{}
	tagVocabWired = false
}

// canonicaliseTags validates and rewrites tags against the wired vocabulary.
// Unwired or empty vocabulary: return input unchanged.
func canonicaliseTags(tags []string) ([]string, error) {
	if !tagVocabWired {
		return tags, nil
	}
	return tagVocabCfg.CanonicaliseTags(tags)
}

// canonicaliseCategory validates and rewrites category against the wired
// vocabulary (sty_b2315e17). Unwired: passthrough so existing verb tests keep
// passing. On reject-mode unknown: named error. Casing is always canonicalised
// when the value EqualFold-matches an allowed entry.
func canonicaliseCategory(cat string) (string, error) {
	if !tagVocabWired {
		return cat, nil
	}
	return tagVocabCfg.CanonicaliseCategory(cat)
}
