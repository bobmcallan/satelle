package agentartifact

import (
	"fmt"
	"sort"
	"strings"
)

// Evidence is what one skill declares its use leaves behind (sty_23ceeca2): the
// markdown sections and JSON keys of the step's output, and the shell commands
// and tools of its run. Selection and wording are authored substrate in the
// skill's frontmatter; satelle only compares the declaration with what the step
// produced, so no provider tool name or command is compiled in.
type Evidence struct {
	// Sections are markdown headings (text after the leading #s) that must appear
	// in the step's output document.
	Sections []string
	// Keys are top-level keys of the final JSON object (dispatched steps).
	Keys []string
	// Commands are command prefixes that must appear among the step's shell
	// commands, matched on the command text.
	Commands []string
	// Tools are tool names that must appear among the step's tool events.
	Tools []string
}

// Declared reports whether the skill declares any evidence at all.
func (e Evidence) Declared() bool {
	return len(e.Sections)+len(e.Keys)+len(e.Commands)+len(e.Tools) > 0
}

// The workflow's `skill_evidence` strictness values (the route source's [meta]):
// whether a skill delivery or evidence gap is only recorded (the default) or
// refuses the transition. Defined here, beside the evidence it governs, so the
// engine that applies it and the structure check that validates it agree.
const (
	EvidenceModeRecord = "record"
	EvidenceModeReject = "reject"
)

// ValidEvidenceMode reports whether v is a recognised skill_evidence value.
func ValidEvidenceMode(v string) bool { return v == EvidenceModeRecord || v == EvidenceModeReject }

// evidenceKeys are the frontmatter keys ParseEvidence reads; any other
// `evidence_*` key is a typo and is refused by name.
var evidenceKeys = []string{"evidence_sections", "evidence_keys", "evidence_commands", "evidence_tools"}

// ParseEvidence reads the evidence_* keys from skill YAML frontmatter. A skill
// that declares none returns the zero Evidence and no error; a malformed
// declaration (an unknown evidence_* key, an empty value, an empty list item or
// an unbalanced bracket) is an error naming the key.
func ParseEvidence(skillBody string) (Evidence, error) {
	fm, ok := frontmatter(skillBody)
	if !ok {
		return Evidence{}, nil
	}
	known := map[string]bool{}
	for _, k := range evidenceKeys {
		known[k] = true
	}
	var unknown []string
	for _, line := range fm {
		key, _, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && strings.HasPrefix(key, "evidence_") && !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Evidence{}, fmt.Errorf("%s: unknown evidence key (supported: %s)",
			strings.Join(unknown, ", "), strings.Join(evidenceKeys, ", "))
	}
	var e Evidence
	for key, dst := range map[string]*[]string{
		"evidence_sections": &e.Sections, "evidence_keys": &e.Keys,
		"evidence_commands": &e.Commands, "evidence_tools": &e.Tools,
	} {
		if !hasKey(fm, key) {
			continue
		}
		items, err := evidenceList(scalar(fm, key))
		if err != nil {
			return Evidence{}, fmt.Errorf("%s: %w", key, err)
		}
		*dst = items
	}
	return e, nil
}

// evidenceList splits a CSV declaration strictly: unlike csv, an empty item is an
// error rather than silently dropped, because a skipped item is evidence the
// author meant to require and the check would never ask for.
func evidenceList(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") != strings.HasSuffix(raw, "]") {
		return nil, fmt.Errorf("unbalanced brackets in %q", raw)
	}
	inner := strings.TrimSpace(strings.Trim(raw, "[]"))
	if inner == "" {
		return nil, fmt.Errorf("declared with no items")
	}
	var out []string
	for _, part := range strings.Split(inner, ",") {
		v := strings.Trim(strings.TrimSpace(part), `"'`)
		if v == "" {
			return nil, fmt.Errorf("empty item in %q", raw)
		}
		out = append(out, v)
	}
	return out, nil
}

// hasKey reports whether the frontmatter declares key, with or without a value.
func hasKey(lines []string, key string) bool {
	prefix := key + ":"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == prefix || strings.HasPrefix(line, prefix+" ") {
			return true
		}
	}
	return false
}
