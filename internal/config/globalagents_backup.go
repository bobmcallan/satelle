package config

import (
	"bytes"
	"strings"
)

// RedactVars removes the top-level [vars] section from a machine-wide profile
// catalog before personal backup (sty_940938e3).
//
// Decision: [vars] is EXCLUDED from backup. It is the operator's secret KV on
// this machine (API keys expanded into profile env at dispatch). satelle has no
// separate personal-secret vault; inventing one is out of scope. The rest of the
// catalog (profiles, roles) is safe to rehydrate onto a new machine; the
// operator re-enters secrets locally after restore.
//
// Redaction is textual, not a TOML round-trip, so comments and formatting of
// non-vars sections survive. Lines from a bare [vars] header through the line
// before the next top-level [section] are replaced with a single marker comment.
func RedactVars(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	// Normalise to \n for scanning; restore original line endings only loosely
	// (catalog files are written with \n).
	text := string(src)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	var out []string
	inVars := false
	wroteMarker := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if isTopLevelTOMLHeader(trim) {
			if isVarsHeader(trim) {
				inVars = true
				if !wroteMarker {
					out = append(out, "# [vars] redacted from personal backup (sty_940938e3) — re-enter secrets locally after restore")
					wroteMarker = true
				}
				continue
			}
			// Any other top-level header ends the vars section.
			inVars = false
		}
		if inVars {
			continue
		}
		out = append(out, line)
	}
	// Preserve a trailing newline when the source had one.
	body := strings.Join(out, "\n")
	if bytes.HasSuffix(src, []byte("\n")) && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return []byte(body)
}

// isTopLevelTOMLHeader reports a bare [section] or [section.sub] line (not
// indented, not a comment). Nested tables under [vars] still start with '[' and
// would end redaction early if we only matched [vars] — we treat any top-level
// '[' line as a section boundary, which is correct for the catalog shape.
func isTopLevelTOMLHeader(trim string) bool {
	if trim == "" || strings.HasPrefix(trim, "#") {
		return false
	}
	return strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]")
}

func isVarsHeader(trim string) bool {
	// [vars] only — not [vars.something] (the catalog does not use nested vars).
	return strings.EqualFold(trim, "[vars]")
}
