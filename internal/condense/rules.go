// Package condense fits resident documents into a context that has a size
// budget (sty_df21b2db). The idea is LLMLingua's, not its package: the prompt is
// compressed when it is assembled, against a budget, with protected tokens kept
// — so an author writes prose at any length and the fit happens at delivery.
//
// Fit is the one owner of the ladder (full → filler → lead → pointer). Every
// word-level decision — which phrases are filler, which tokens are protected,
// where a clause or a sentence ends, the order of the rungs — is a Rules value
// parsed from configuration (internal/config/substrate/config/condense.toml,
// overridable per repo); this package recognises only markdown structure: code
// fences, inline code, headings, [[links]] and list items. The authored text is
// never changed: a condensed body carries a `satelle retrieve` marker whose
// stored bytes are the original.
package condense

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Rules is the condensing configuration. Every field is read from TOML; none has
// a Go default.
type Rules struct {
	Ladder              []string `toml:"ladder"`
	DropPhrases         []string `toml:"drop_phrases"`
	ProtectedPatterns   []string `toml:"protected_patterns"`
	ClauseDelimiters    []string `toml:"clause_delimiters"`
	SentenceTerminators []string `toml:"sentence_terminators"`
	SentenceExceptions  []string `toml:"sentence_exceptions"`

	compiled *compiledRules
}

type compiledRules struct {
	drop      *regexp.Regexp
	protected []*regexp.Regexp
}

// The rung names Go knows how to perform. The ORDER of the rungs, and whether a
// rung is used at all, is configuration; what each name does is mechanism.
const (
	RungFull    = "full"
	RungFiller  = "filler"
	RungLead    = "lead"
	RungPointer = "pointer"
)

// ParseRules layers TOML documents over each other, first to last: a key present
// in a later layer replaces the earlier value, a key it omits is kept. The first
// layer is the embedded default; later ones are repo overrides. The result is
// validated and its patterns compiled.
func ParseRules(layers ...[]byte) (Rules, error) {
	var r Rules
	for i, raw := range layers {
		if len(raw) == 0 {
			continue
		}
		if _, err := toml.Decode(string(raw), &r); err != nil {
			return Rules{}, fmt.Errorf("condense rules (layer %d): %w", i, err)
		}
	}
	if err := r.compile(); err != nil {
		return Rules{}, err
	}
	return r, nil
}

func (r *Rules) compile() error {
	if len(r.Ladder) == 0 {
		return fmt.Errorf("condense rules: no ladder declared")
	}
	if r.Ladder[0] != RungFull {
		return fmt.Errorf("condense rules: the ladder must start at %q, got %q", RungFull, r.Ladder[0])
	}
	for i, name := range r.Ladder {
		switch name {
		case RungFull, RungFiller, RungLead:
		case RungPointer:
			if i != len(r.Ladder)-1 {
				return fmt.Errorf("condense rules: %q must be the last rung", RungPointer)
			}
		default:
			return fmt.Errorf("condense rules: unknown rung %q", name)
		}
	}
	c := &compiledRules{}
	var alts []string
	for _, p := range r.DropPhrases {
		if p = strings.TrimSpace(p); p != "" {
			alts = append(alts, regexp.QuoteMeta(p))
		}
	}
	if len(alts) > 0 {
		re, err := regexp.Compile(`(?i)\b(?:` + strings.Join(alts, "|") + `)\b[ \t]?`)
		if err != nil {
			return fmt.Errorf("condense rules: drop_phrases: %w", err)
		}
		c.drop = re
	}
	for _, p := range r.ProtectedPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("condense rules: protected pattern %q: %w", p, err)
		}
		c.protected = append(c.protected, re)
	}
	r.compiled = c
	return nil
}

// Unset reports whether r is the zero Rules, which callers replace by the
// embedded defaults.
func (r Rules) Unset() bool { return len(r.Ladder) == 0 }

// ready returns r with its patterns compiled; a Rules built by hand rather than
// by ParseRules is compiled on first use.
func (r Rules) ready() (Rules, error) {
	if r.compiled != nil {
		return r, nil
	}
	if err := r.compile(); err != nil {
		return Rules{}, err
	}
	return r, nil
}

// hasRung reports whether the ladder declares name.
func (r Rules) hasRung(name string) bool {
	for _, n := range r.Ladder {
		if n == name {
			return true
		}
	}
	return false
}
