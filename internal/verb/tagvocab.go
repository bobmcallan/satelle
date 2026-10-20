package verb

import (
	"fmt"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/workitem"
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

// referencesOf reads the requirement references off a story's tags under the
// wired [tags] references namespaces (sty_12499ecc). Unwired, or with no
// namespace declared, a tag is only a tag.
func referencesOf(tags []string) []config.Reference {
	if !tagVocabWired {
		return nil
	}
	return tagVocabCfg.References(tags)
}

// referencesSection renders the references appended to the route view, read live
// from the story's tags so it is never stale. Empty when the story has none, so a
// route without references reads exactly as before.
func referencesSection(item workitem.Item) string {
	refs := referencesOf(item.Tags)
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## References\n\n")
	for _, r := range refs {
		fmt.Fprintf(&b, "- %s (`%s`)\n", r, r.Tag)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
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
