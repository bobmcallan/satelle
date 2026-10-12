// wikilink.go — deterministic [[wikilink]] resolution for authored substrate.
//
// This is the ONE implementation of `[[name]]` resolution: `satelle validate`
// and `satelle doctor` both call it, so the two surfaces cannot disagree about
// which references resolve.
//
// The pull channel depends on [[name]] references; until this check existed,
// dangling links silently killed on-demand discoverability. Every wikilink in
// substrate (principles, skills, workflows, documents, constitution) must
// resolve to a known artifact name across kinds (disk + embedded defaults).
//
// Repo-agnostic: catalog is built from dataDir + EmbeddedDefaults only. Bash
// `[[` tests are excluded (target must start with an alphanumeric — character
// classes like [[:space:]] do not match). Quoted code — fenced blocks and
// inline spans — is masked before the scan: a doc that SHOWS `[[gate]]` is
// naming a TOML array-of-tables header, not referring to a document.
package ctxaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// WikiLinkRe matches markdown wikilinks [[target]] or [[target|label]] /
// [[target#anchor]]. Target must start with a letter or digit so bash tests
// like [[ -z "$x" ]] and [[:space:]] are not treated as links.
var WikiLinkRe = regexp.MustCompile(`\[\[([A-Za-z0-9][^\]|#\n]*)(?:[|#][^\]]*)?\]\]`)

// SubstrateKindsScanned are the authored-markdown kinds whose bodies are
// scanned for wikilinks and whose basenames form the resolution catalog.
var SubstrateKindsScanned = []string{"principles", "skills", "workflows", "documents", "tasks"}

// AuditWikilinks returns problem strings for every dangling [[target]] under
// dataDir's substrate + constitution, plus embedded defaults' bodies when they
// are not already on disk. Empty means clean.
func AuditWikilinks(dataDir string, embedded []config.EmbeddedDefault) []string {
	catalog := Catalog(dataDir, embedded)
	var problems []string

	// Scan on-disk substrate.
	for _, kind := range SubstrateKindsScanned {
		dir := filepath.Join(dataDir, kind)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
				return nil
			}
			// Skip generated / reserved keep files? README is fine to scan.
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			rel, _ := filepath.Rel(dataDir, path)
			problems = append(problems, Dangling(string(body), rel, catalog)...)
			return nil
		})
	}
	// Constitution (lives at dataDir root, not under a kind).
	if body, err := os.ReadFile(filepath.Join(dataDir, config.DefaultConstitutionName)); err == nil {
		problems = append(problems, Dangling(string(body), config.DefaultConstitutionName, catalog)...)
	}
	// Embedded bodies not present on disk still ship in other repos — scan them
	// under a virtual path so a dangler in an embedded default fails validate.
	for _, d := range embedded {
		if d.Kind == "" || d.Name == "" {
			continue
		}
		// TOML has NO wikilinks, and scanning it produces guaranteed false
		// positives: an array-of-tables header is spelled `[[category]]`, which is
		// character-for-character a wikilink. That is TOML structure, not a
		// reference to a document that ought to exist.
		if !strings.EqualFold(d.Ext, ".md") && d.Ext != "" {
			continue
		}
		onDisk := filepath.Join(dataDir, filepath.FromSlash(d.RelPath()))
		if _, err := os.Stat(onDisk); err == nil {
			continue // already scanned
		}
		virt := "embedded:" + d.RelPath()
		problems = append(problems, Dangling(d.Body, virt, catalog)...)
	}

	sort.Strings(problems)
	return problems
}

// Catalog is the set of names a [[target]] may resolve to: basename of
// every substrate .md (any kind), constitution aliases, and every embedded
// default name. Cross-kind: [[satelle-agent-model]] resolves whether it is a
// principle or a document.
func Catalog(dataDir string, embedded []config.EmbeddedDefault) map[string]bool {
	cat := map[string]bool{
		// Constitution is not a principle; both common spellings resolve.
		"satelle-constitution": true,
		"constitution":         true,
	}
	for _, kind := range SubstrateKindsScanned {
		dir := filepath.Join(dataDir, kind)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
				return nil
			}
			base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			if base != "" && !strings.EqualFold(base, "README") {
				cat[base] = true
			}
			return nil
		})
	}
	for _, d := range embedded {
		if d.Name != "" {
			cat[d.Name] = true
		}
	}
	return cat
}

// codeSpanRe matches an inline `code span`. Text inside one is QUOTED, not
// authored: a doc that shows `[[gate]]` is naming a TOML array-of-tables header,
// not referring to a document called "gate". Masking spans before the wikilink
// scan is what lets prose talk about syntax without every example becoming a
// dangling link.
var codeSpanRe = regexp.MustCompile("`[^`\n]*`")

// fencedBlockRe matches a fenced code block: up to three spaces of indent, an
// opening run of three or more backticks or tildes with an optional info
// string, the body, and a closing fence — or end of input when the fence is
// never closed. A regex is the proportionate tool for a substrate linter;
// nested and list-indented fences are out of scope.
var fencedBlockRe = regexp.MustCompile("(?ms)^ {0,3}(?:`{3,}|~{3,})[^\n]*\n.*?(?:^ {0,3}(?:`{3,}|~{3,})[^\n]*$|\\z)")

// MaskFenced blanks the contents of fenced code blocks only, byte-for-byte
// (see MaskQuotedCode). Backtick-name extraction masks fences but must keep
// inline spans, since a backticked name IS an inline span.
func MaskFenced(body string) string {
	return fencedBlockRe.ReplaceAllStringFunc(body, blank)
}

// blank replaces every non-newline BYTE with a space. Byte-wise, not rune-wise:
// strings.Map would collapse a multi-byte rune to a one-byte space and break
// the offset invariant.
func blank(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

// MaskQuotedCode blanks the contents of fenced code blocks and inline code
// spans, returning a copy of body with the SAME byte length (non-newline bytes
// become spaces, newlines are kept). Length preservation is load-bearing:
// the migrate rewriter finds matches in the masked copy and slices the
// original by those offsets, so a mask that removed bytes would corrupt files.
// Fences are masked first so a stray backtick inside one cannot pair across the
// fence boundary.
func MaskQuotedCode(body string) string {
	return codeSpanRe.ReplaceAllStringFunc(MaskFenced(body), blank)
}

// LinkTargets returns the distinct [[target]] names in body, in order of first
// appearance, with quoted code masked and work-item ids (sty_… / tsk_…) skipped —
// those are not substrate pull targets.
func LinkTargets(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range WikiLinkRe.FindAllStringSubmatch(MaskQuotedCode(body), -1) {
		target := strings.TrimSpace(m[1])
		if target == "" || seen[target] {
			continue
		}
		if strings.HasPrefix(target, "sty_") || strings.HasPrefix(target, "tsk_") {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}

// DanglingTargets returns the [[target]] names in body that the catalog does not
// resolve.
func DanglingTargets(body string, catalog map[string]bool) []string {
	var out []string
	for _, target := range LinkTargets(body) {
		if !catalog[target] {
			out = append(out, target)
		}
	}
	return out
}

// RetirementSuffix is the operator-facing note appended to a problem that names
// a retired document, or "" when name is not retired.
func RetirementSuffix(name string) string {
	if e, ok := config.LookupRetired(name); ok {
		return " — " + config.FormatRetirementMessage(name, e)
	}
	return ""
}

// Dangling returns problem lines for unresolved wikilinks in body.
func Dangling(body, where string, catalog map[string]bool) []string {
	var out []string
	for _, target := range DanglingTargets(body, catalog) {
		// Enrich with the retirement map so an operator does not have to
		// diagnose a binary-side rename. Still a FAIL.
		out = append(out, fmt.Sprintf("%s: dangling wikilink [[%s]]", where, target)+RetirementSuffix(target))
	}
	return out
}
