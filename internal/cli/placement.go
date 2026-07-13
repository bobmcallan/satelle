// placement.go — deterministic substrate placement validator
// (epic:substrate-convergence order:6).
//
// Guards four post-order-1–5 invariants so placement cannot silently drift:
//  1. embedded-owned on-disk copies that still match the embedded body carry
//     embedded_sha (order:1 provenance)
//  2. the only legal principles:* tag is principles:session (order:3 residency)
//  3. no inert scope: key on principles (order:3 removed that axis)
//  4. resident set + constitution stay under alwaysContextCeiling (order:4 diet)
//
// Anchored under `satelle principle validate` (whole-set, nameFilter empty) —
// same pattern as workflow consistency. Complements the semantic
// satelle-substrate-audit skill; this package is code-only, repo-agnostic
// (no this-repo story ids; driven by EmbeddedDefaults, sessionTag, ceiling).
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/docindex"
)

// auditPlacement runs the placement checks over dataDir (.satelle).
// embedded is typically config.EmbeddedDefaults(); constitution is the
// already-read constitution body (may be empty). Returns problem strings;
// empty means clean. Includes unknown tag axes on principles.
func auditPlacement(dataDir string, embedded []config.EmbeddedDefault, constitution string) []string {
	var out []string
	out = append(out, checkEmbeddedStamps(dataDir, embedded)...)
	out = append(out, checkResidencyMarkers(dataDir)...)
	out = append(out, checkPrincipleScope(dataDir)...)
	out = append(out, checkUnknownTagAxes(dataDir)...)
	out = append(out, checkResidentCeiling(dataDir, constitution)...)
	return out
}

// legalPrincipleTagAxes is the whitelist of tag axes allowed on principles.
// Residency uses principles:session; type:principle (or type:workflow etc.) is
// the kind marker. Any other colon-axis (kind:, principles:global, …) is inert
// or invented and must be flagged (init substrate analysis AC2).
var legalPrincipleTagAxes = map[string]bool{
	"type":       true,
	"principles": true, // only principles:session is legal; value checked separately
}

// checkUnknownTagAxes flags principle tags whose axis (prefix before first `:`)
// is outside the legalPrincipleTagAxes whitelist. PRINCIPLES-scoped only —
// story/task tags freely use epic:/sprint:/order:.
func checkUnknownTagAxes(dataDir string) []string {
	var out []string
	for _, path := range principleFiles(dataDir) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		for _, tag := range frontmatterTags(string(body)) {
			axis, _, ok := strings.Cut(tag, ":")
			if !ok {
				continue // bare tag without axis — leave alone
			}
			if !legalPrincipleTagAxes[axis] {
				out = append(out, fmt.Sprintf(
					"principles/%s: unknown tag axis %q on tag %q (legal axes on principles: type, principles)",
					name, axis, tag))
			}
		}
	}
	return out
}

// checkEmbeddedStamps: an on-disk file identical to an embedded default must
// carry embedded_sha. Operator overrides (different body, no stamp) are OK.
func checkEmbeddedStamps(dataDir string, embedded []config.EmbeddedDefault) []string {
	var out []string
	for _, d := range embedded {
		rel := filepath.Join(d.Kind, d.Name+".md")
		path := filepath.Join(dataDir, rel)
		body, err := os.ReadFile(path)
		if err != nil {
			continue // absent is not drift
		}
		stripped, _, stamped := stripEmbeddedStamp(string(body))
		if stamped {
			continue
		}
		if embeddedSHA(stripped) == embeddedSHA(d.Body) {
			out = append(out, fmt.Sprintf(
				"%s/%s: embedded-owned copy identical to the default but missing embedded_sha",
				d.Kind, d.Name))
		}
	}
	return out
}

// checkResidencyMarkers: only principles:session is a legal principles:* tag.
func checkResidencyMarkers(dataDir string) []string {
	var out []string
	for _, path := range principleFiles(dataDir) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		for _, tag := range frontmatterTags(string(body)) {
			if strings.HasPrefix(tag, "principles:") && tag != sessionTag {
				out = append(out, fmt.Sprintf(
					"principles/%s: illegal residency-ish tag %q (only %q is the system marker)",
					name, tag, sessionTag))
			}
		}
	}
	return out
}

// checkPrincipleScope: no scope: key on principles frontmatter.
func checkPrincipleScope(dataDir string) []string {
	var out []string
	for _, path := range principleFiles(dataDir) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if frontmatterHasKey(string(body), "scope") {
			name := strings.TrimSuffix(filepath.Base(path), ".md")
			out = append(out, fmt.Sprintf(
				"principles/%s: inert scope: on a principle — residency is the %s tag alone",
				name, sessionTag))
		}
	}
	return out
}

// checkResidentCeiling: constitution + session-tagged principles must fit under
// alwaysContextCeiling (same renderer as SessionStart injection).
func checkResidentCeiling(dataDir string, constitution string) []string {
	var docs []docindex.Doc
	for _, path := range principleFiles(dataDir) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !docHasTag(string(body), sessionTag) {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		docs = append(docs, docindex.Doc{Kind: "principles", Name: name, Body: string(body)})
	}
	_, truncated := renderAlwaysContent(constitution, docs, alwaysContextCeiling)
	if truncated {
		return []string{fmt.Sprintf(
			"resident principle set + constitution exceeds the SessionStart ceiling (%d bytes); trim a session-tagged principle",
			alwaysContextCeiling)}
	}
	return nil
}

// principleFiles lists dataDir/principles/*.md, skipping reserved keep-files.
func principleFiles(dataDir string) []string {
	dir := filepath.Join(dataDir, "principles")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		fn := e.Name()
		if e.IsDir() || !strings.HasSuffix(fn, ".md") || reservedKeepFile(fn) {
			continue
		}
		out = append(out, filepath.Join(dir, fn))
	}
	return out
}

// frontmatterHasKey reports whether the YAML frontmatter contains a bare key
// line (e.g. "scope: …"). Ignores nested content; sufficient for the inert
// top-level scope axis on principles.
func frontmatterHasKey(body, key string) bool {
	fm := frontmatter(body)
	if fm == "" {
		return false
	}
	prefix := key + ":"
	for _, line := range strings.Split(fm, "\n") {
		t := strings.TrimSpace(line)
		if t == prefix || strings.HasPrefix(t, prefix+" ") || strings.HasPrefix(t, prefix+"\t") {
			return true
		}
	}
	return false
}
