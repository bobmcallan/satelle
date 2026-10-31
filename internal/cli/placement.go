// placement.go — deterministic substrate placement validator
// (epic:substrate-convergence order:6).
//
// Guards four post-order-1–5 invariants so placement cannot silently drift:
//  1. embedded-owned on-disk copies that still match the embedded body carry
//     embedded_sha (order:1 provenance)
//  2. the only legal principles:* tags are principles:session and
//     principles:seat (order:3 residency)
//  3. no inert scope: key on principles (order:3 removed that axis)
//
// The resident-set CEILING is not among them: how large the set may be depends
// on the harness that receives it, and a set a harness degrades to read
// pointers is advice, not a defect that stops a repo. It is judged per bound
// harness by residentCeilingWarnings, at WARN severity, wherever it is shown.
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
)

// auditPlacement runs the placement checks over dataDir (.satelle).
// embedded is typically config.EmbeddedDefaults(). Returns problem strings;
// empty means clean. Includes unknown tag axes on principles. Every one is a
// FAIL; the advisory resident-set ceiling is residentCeilingWarnings.
func auditPlacement(dataDir string, embedded []config.EmbeddedDefault) []string {
	var out []string
	out = append(out, checkEmbeddedStamps(dataDir, embedded)...)
	out = append(out, checkResidencyMarkers(dataDir)...)
	out = append(out, checkPrincipleScope(dataDir)...)
	out = append(out, checkUnknownTagAxes(dataDir)...)
	return out
}

// legalPrincipleTagAxes is the whitelist of tag axes allowed on principles.
// Residency uses principles:session; type:principle (or type:workflow etc.) is
// the kind marker. Any other colon-axis (kind:, principles:global, …) is inert
// or invented and must be flagged (init substrate analysis AC2).
var legalPrincipleTagAxes = map[string]bool{
	"type":       true,
	"principles": true, // only principles:session and principles:seat are legal; value checked separately
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
		rel := filepath.FromSlash(d.RelPath())
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

// checkResidencyMarkers: only principles:session and principles:seat are legal
// principles:* tags.
func checkResidencyMarkers(dataDir string) []string {
	var out []string
	for _, path := range principleFiles(dataDir) {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		for _, tag := range frontmatterTags(string(body)) {
			if strings.HasPrefix(tag, "principles:") && tag != sessionTag && tag != config.SeatResidencyTag {
				out = append(out, fmt.Sprintf(
					"principles/%s: illegal residency-ish tag %q (only %q, the system marker, and %q, the seat-only marker, are legal)",
					name, tag, sessionTag, config.SeatResidencyTag))
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

// sessionFit is how one bound harness receives the SessionStart context: the
// size rendered under its limit and what degraded to read pointers.
type sessionFit struct {
	Harness  harnessBinding
	Rendered int
	Omitted  []string
}

// sessionFits renders the SessionStart context once for each bound harness —
// the same renderer and limit as `satelle hook context --harness <h>` — and
// returns the fit of each, plus the harnesses that could not be judged. It is
// the single measurement `satelle doctor`, `satelle init` and `satelle
// principle validate` report from.
func sessionFits(dataDir, repoRoot string) ([]sessionFit, []harnessGap) {
	harnesses, gaps := boundHarnesses(dataDir, repoRoot)
	fits := make([]sessionFit, 0, len(harnesses))
	for _, h := range harnesses {
		content, omitted := renderSessionContext(dataDir, h)
		fits = append(fits, sessionFit{Harness: h, Rendered: measureContext(content, h), Omitted: omitted})
	}
	return fits, gaps
}

// residentCeilingWarnings is the ONE place the resident-set ceiling is turned
// into a message, and the one place it is downgraded to advice: a set that
// overflows a bound harness's limit degrades to read pointers there (the agent
// is told in-context), which `satelle init` and `satelle principle validate`
// show as WARN. Doctor reports the same measurement under its own identifier.
func residentCeilingWarnings(dataDir, repoRoot string) []string {
	fits, _ := sessionFits(dataDir, repoRoot)
	var out []string
	for _, f := range fits {
		if len(f.Omitted) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf(
			"resident principle set + constitution exceeds the %s SessionStart limit (%d %s; omitted: %s); trim a session-tagged principle",
			f.Harness.Name, f.Harness.Limit, f.Harness.Unit, strings.Join(f.Omitted, ", ")))
	}
	return out
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
