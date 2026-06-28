package docindex

// Open Knowledge Format (OKF v0.1) support for the documents store.
//
// OKF (GoogleCloudPlatform/knowledge-catalog/okf) represents knowledge as plain
// markdown files with YAML frontmatter. A concept document's single REQUIRED
// frontmatter key is `type` (a free-form categorising string — no central enum);
// `title`, `description`, `tags`, and `timestamp` (ISO 8601) are recommended.
// The filenames `index.md` (progressive-disclosure link list, no required
// frontmatter) and `log.md` (date-grouped changelog) are RESERVED and are not
// concept documents.
//
// satelle has no single Go writer for `.satelle/documents` (e.g. commit
// summaries are authored ad-hoc), so — exactly as workflows are normalised to
// the DOT standard at ingest — documents are normalised to OKF frontmatter at
// ingest. That back-fills every document regardless of who wrote it, and the
// bundle-root index.md is regenerated after each sync.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const okfVersion = "0.1"

// okfReserved reports whether a document name (filename without .md) is an OKF
// reserved file (index, log) rather than a concept.
func okfReserved(name string) bool { return name == "index" || name == "log" }

// splitFrontmatter splits a markdown body into its YAML frontmatter lines (the
// content between a leading `---` fence and the next `---`) and the remaining
// body. ok is false when there is no terminated leading frontmatter block.
func splitFrontmatter(body string) (fm []string, rest string, ok bool) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, body, false
	}
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			return lines[1:j], strings.Join(lines[j+1:], "\n"), true
		}
	}
	return nil, body, false // unterminated → treat as no frontmatter
}

// fmScalar returns the unquoted top-level scalar value for key in the
// frontmatter lines, or "" if the key is absent or has no inline value.
func fmScalar(fm []string, key string) string {
	pre := key + ":"
	for _, ln := range fm {
		t := strings.TrimSpace(ln)
		if t == pre || strings.HasPrefix(t, pre+" ") {
			return yamlUnquote(strings.TrimSpace(strings.TrimPrefix(t, pre)))
		}
	}
	return ""
}

// OKFConformance checks a single documents file for OKF v0.1 conformance: a
// concept document must carry YAML frontmatter with a non-empty `type`.
// Reserved files (index.md, log.md) are exempt. Returns nil when conformant.
func OKFConformance(name, body string) error {
	if okfReserved(name) {
		return nil
	}
	fm, _, ok := splitFrontmatter(body)
	if !ok {
		return fmt.Errorf("missing YAML frontmatter (OKF requires a `type`)")
	}
	if fmScalar(fm, "type") == "" {
		return fmt.Errorf("frontmatter missing the required non-empty `type` key")
	}
	return nil
}

// normalizeOKFDir rewrites every frontmatter-less or type-less concept file
// under the documents directory (recursively, matching how walkMarkdown indexes)
// with OKF frontmatter, in place. Best-effort and idempotent: reserved and
// already-conformant files are left untouched, and a read/write error on one
// file does not stop the others. Running it before the sync's skip-unchanged
// check is what back-fills documents indexed by an earlier build.
func normalizeOKFDir(dir string) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if okfReserved(name) {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		mod := time.Now()
		if info, ierr := d.Info(); ierr == nil {
			mod = info.ModTime()
		}
		if out, changed := normalizeOKF(name, string(body), mod); changed {
			_ = os.WriteFile(path, []byte(out), 0o644)
		}
		return nil
	})
}

// normalizeOKF ensures a concept document carries OKF frontmatter with a
// non-empty `type`, returning the (possibly rewritten) body and whether it
// changed. Reserved files and already-conformant documents are returned
// unchanged, so the call is idempotent. For a frontmatter-less or type-less
// document the metadata is derived: type from the filename, title from the
// first heading, description from the first prose line, timestamp from modtime.
func normalizeOKF(name, body string, mod time.Time) (string, bool) {
	if okfReserved(name) {
		return body, false
	}
	fm, rest, ok := splitFrontmatter(body)
	if ok && fmScalar(fm, "type") != "" {
		return body, false // already OKF-conformant
	}
	get := func(k, def string) string {
		if ok {
			if v := fmScalar(fm, k); v != "" {
				return v
			}
		}
		return def
	}
	bodyPart := rest
	if !ok {
		bodyPart = body
	}
	typ := get("type", deriveType(name))
	title := get("title", deriveTitle(body, name))
	desc := get("description", deriveDescription(bodyPart))
	ts := get("timestamp", mod.UTC().Format(time.RFC3339))
	if desc == "" {
		desc = title
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "type: %s\n", yamlScalar(typ))
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(title))
	fmt.Fprintf(&b, "description: %s\n", yamlScalar(desc))
	fmt.Fprintf(&b, "tags:\n- %s\n", yamlScalar(typ))
	fmt.Fprintf(&b, "timestamp: %s\n", yamlScalar(ts))
	// Preserve any other pre-existing top-level scalar keys we didn't synthesize
	// (best-effort; list-valued continuations are skipped to avoid corruption).
	if ok {
		for _, ln := range fm {
			t := strings.TrimSpace(ln)
			if t == "" || strings.HasPrefix(t, "- ") {
				continue
			}
			if isSynthesizedKey(t) {
				continue
			}
			b.WriteString(ln + "\n")
		}
	}
	b.WriteString("---\n\n")
	out := b.String() + strings.TrimLeft(bodyPart, "\n")
	return out, out != body
}

func isSynthesizedKey(line string) bool {
	for _, k := range []string{"type:", "title:", "description:", "tags:", "timestamp:"} {
		if line == strings.TrimSuffix(k, ":") || strings.HasPrefix(line, k) {
			return true
		}
	}
	return false
}

// isOrderedListItem reports whether t begins like an ordered-list marker
// ("1. ", "2) "), so a numbered acceptance criterion isn't mistaken for prose.
func isOrderedListItem(t string) bool {
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	return i > 0 && i < len(t) && (t[i] == '.' || t[i] == ')')
}

func deriveType(name string) string {
	if strings.HasPrefix(name, "commit-summary") {
		return "commit-summary"
	}
	return "document"
}

// deriveTitle returns the document's first heading (frontmatter skipped) or, if
// there is none, a humanised form of the filename.
func deriveTitle(body, name string) string {
	if h := headline(body); h != "" {
		return h
	}
	return strings.ReplaceAll(name, "-", " ")
}

// deriveDescription returns the first plain prose line — a paragraph, not a
// heading, list item, blockquote, table, or code — truncated to one line. It
// returns "" when the body has no such line (e.g. an all-bullets commit
// summary), so the caller falls back to the title.
func deriveDescription(body string) string {
	inCode := false
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") {
			inCode = !inCode
			continue
		}
		if inCode || t == "" {
			continue
		}
		switch t[0] {
		case '#', '-', '*', '>', '|', '+', '=': // heading/list/quote/table/rule
			continue
		}
		if isOrderedListItem(t) { // "1. …" / "2) …"
			continue
		}
		if len(t) > 200 {
			t = strings.TrimSpace(t[:200]) + "…"
		}
		return t
	}
	return ""
}

// yamlScalar renders v as a YAML scalar, single-quoting (and escaping internal
// quotes) when it contains characters that would otherwise need it.
func yamlScalar(v string) string {
	if v == "" {
		return "''"
	}
	if v != strings.TrimSpace(v) || strings.ContainsAny(v, ":#'\"[]{}&*!|>%@,`\n") || v[0] == '-' {
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return v
}

// yamlUnquote reverses single/double quoting of a YAML scalar value.
func yamlUnquote(v string) string {
	if len(v) >= 2 {
		if v[0] == '\'' && v[len(v)-1] == '\'' {
			return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
		if v[0] == '"' && v[len(v)-1] == '"' {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// writeOKFIndex regenerates the bundle-root index.md for the documents directory
// from the indexed concept documents — a progressive-disclosure link list with
// the root declaring okf_version. It is written only when its content changes
// (so it converges and does not churn the index), and is skipped entirely when
// there are no concepts and no existing index.
func (s *Store) writeOKFIndex(dir string, docs []Doc) error {
	type entry struct{ name, title, desc string }
	var entries []entry
	for _, d := range docs {
		if d.Kind != "documents" || okfReserved(d.Name) {
			continue
		}
		fm, _, _ := splitFrontmatter(d.Body)
		title := fmScalar(fm, "title")
		if title == "" {
			title = d.Headline
		}
		if title == "" {
			title = d.Name
		}
		entries = append(entries, entry{d.Name, title, fmScalar(fm, "description")})
	}
	path := filepath.Join(dir, "index.md")
	if len(entries) == 0 {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil // nothing to list and no stale index to maintain
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var b strings.Builder
	fmt.Fprintf(&b, "---\nokf_version: \"%s\"\n---\n\n# Documents\n\n", okfVersion)
	for _, e := range entries {
		if e.desc != "" && e.desc != e.title { // omit a description that just echoes the title
			fmt.Fprintf(&b, "* [%s](%s.md) - %s\n", e.title, e.name, e.desc)
		} else {
			fmt.Fprintf(&b, "* [%s](%s.md)\n", e.title, e.name)
		}
	}
	content := b.String()
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return nil // unchanged
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
