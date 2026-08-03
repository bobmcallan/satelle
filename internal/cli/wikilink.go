// wikilink.go — deterministic [[wikilink]] resolution for authored substrate.
//
// The pull channel depends on [[name]] references; until this check existed,
// dangling links silently killed on-demand discoverability. Every wikilink in
// substrate (principles, skills, workflows, documents, constitution) must
// resolve to a known artifact name across kinds (disk + embedded defaults).
//
// Repo-agnostic: catalog is built from dataDir + EmbeddedDefaults only. Bash
// `[[` tests are excluded (target must start with an alphanumeric — character
// classes like [[:space:]] do not match).
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bobmcallan/satelle/internal/config"
)

// wikiLinkRe matches markdown wikilinks [[target]] or [[target|label]] /
// [[target#anchor]]. Target must start with a letter or digit so bash tests
// like [[ -z "$x" ]] and [[:space:]] are not treated as links.
var wikiLinkRe = regexp.MustCompile(`\[\[([A-Za-z0-9][^\]|#\n]*)(?:[|#][^\]]*)?\]\]`)

// substrateKindsScanned are the authored-markdown kinds whose bodies are
// scanned for wikilinks and whose basenames form the resolution catalog.
var substrateKindsScanned = []string{"principles", "skills", "workflows", "documents", "tasks"}

// auditWikilinks returns problem strings for every dangling [[target]] under
// dataDir's substrate + constitution, plus embedded defaults' bodies when they
// are not already on disk. Empty means clean.
func auditWikilinks(dataDir string, embedded []config.EmbeddedDefault) []string {
	catalog := wikilinkCatalog(dataDir, embedded)
	var problems []string

	// Scan on-disk substrate.
	for _, kind := range substrateKindsScanned {
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
			problems = append(problems, danglingIn(string(body), rel, catalog)...)
			return nil
		})
	}
	// Constitution (lives at dataDir root, not under a kind).
	if body, err := os.ReadFile(filepath.Join(dataDir, config.DefaultConstitutionName)); err == nil {
		problems = append(problems, danglingIn(string(body), config.DefaultConstitutionName, catalog)...)
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
		problems = append(problems, danglingIn(d.Body, virt, catalog)...)
	}

	sort.Strings(problems)
	return problems
}

// wikilinkCatalog is the set of names a [[target]] may resolve to: basename of
// every substrate .md (any kind), constitution aliases, and every embedded
// default name. Cross-kind: [[satelle-agent-model]] resolves whether it is a
// principle or a document.
func wikilinkCatalog(dataDir string, embedded []config.EmbeddedDefault) map[string]bool {
	cat := map[string]bool{
		// Constitution is not a principle; both common spellings resolve.
		"satelle-constitution": true,
		"constitution":         true,
	}
	for _, kind := range substrateKindsScanned {
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
// not referring to a document called "gate". Stripping spans before the wikilink
// scan is what lets prose talk about syntax without every example becoming a
// dangling link.
var codeSpanRe = regexp.MustCompile("`[^`\n]*`")

// danglingIn returns problem lines for unresolved wikilinks in body.
func danglingIn(body, where string, catalog map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	body = codeSpanRe.ReplaceAllString(body, "")
	for _, m := range wikiLinkRe.FindAllStringSubmatch(body, -1) {
		target := strings.TrimSpace(m[1])
		if target == "" || catalog[target] {
			continue
		}
		// Skip work-item ids (sty_… / tsk_…) — not substrate pull targets.
		if strings.HasPrefix(target, "sty_") || strings.HasPrefix(target, "tsk_") {
			continue
		}
		key := where + "\x00" + target
		if seen[key] {
			continue
		}
		seen[key] = true
		line := fmt.Sprintf("%s: dangling wikilink [[%s]]", where, target)
		// Enrich with the retirement map so an operator does not have to
		// diagnose a binary-side rename. Still a FAIL.
		if e, ok := config.LookupRetired(target); ok {
			line += " — " + config.FormatRetirementMessage(target, e)
		}
		out = append(out, line)
	}
	return out
}
