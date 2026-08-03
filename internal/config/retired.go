package config

// RetiredSubstrate records embedded substrate documents the binary has
// retired. Authored repos may still cite the old name via [[wikilink]]; the
// tooling uses this map to tell the operator what happened and (for renames)
// to offer an opt-in rewrite via `satelle migrate --yes`.
//
// RETIREMENT OBLIGATION (sty_55a6aaeb AC4): when you delete or rename an
// embedded default under internal/config/substrate/, ADD a row here in the
// same change. A retirement without a row re-creates the dangling-wikilink
// gap. Replacement empty means removed with no redirect — validate/migrate
// report only, never rewrite.
//
// Modelled on the CLI's retiredNames map (cmd surface renames); this map is
// substrate-name renames shipped inside the binary.
type RetiredEntry struct {
	// Kind is the substrate kind the name lived under (principles, skills, …).
	Kind string
	// Version is the satelle release that retired it (for operator messages).
	Version string
	// Replacement is the live name to redirect to, or "" when removed with no
	// successor. Non-empty values must resolve to a current EmbeddedDefault.
	Replacement string
	// Note is short operator-facing context (why / where the content went).
	Note string
}

// RetiredSubstrate is the canonical map of retired embedded names.
// Keys are basenames (no .md, no kind prefix) — the same form as [[wikilinks]].
var RetiredSubstrate = map[string]RetiredEntry{
	"satelle-dot-standard": {
		Kind:        "principles",
		Version:     "0.0.385",
		Replacement: "satelle-route-standard",
		Note:        "the DOT format the binary no longer reads",
	},
	"satelle-configuration-over-code": {
		Kind:        "principles",
		Version:     "0.0.11", // embedded default deleted; content folded into the constitution
		Replacement: "",
		Note:        "folded into the constitution's \"Configuration over code\" section",
	},
}

// LookupRetired returns the retirement entry for name, if any.
func LookupRetired(name string) (RetiredEntry, bool) {
	e, ok := RetiredSubstrate[name]
	return e, ok
}

// FormatRetirementMessage is the operator-facing suffix for a dangling
// wikilink that names a retired document.
func FormatRetirementMessage(name string, e RetiredEntry) string {
	if e.Replacement != "" {
		msg := "retired in " + e.Version + ", renamed to [[" + e.Replacement + "]]"
		if e.Note != "" {
			msg += " (" + e.Note + ")"
		}
		msg += " — run `satelle migrate --yes` to rewrite authored [[refs]], or edit by hand"
		return msg
	}
	msg := "retired in " + e.Version + ", no replacement"
	if e.Note != "" {
		msg += ": " + e.Note
	}
	msg += " — rewrite the citing prose by hand (migrate will not invent a substitute)"
	return msg
}
