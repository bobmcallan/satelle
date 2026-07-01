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
func okfReserved(name string) bool {
	return name == "index" || name == "log" || strings.EqualFold(name, "README")
}

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

// authoredType maps an authored-substrate directory kind to its OKF `type`
// value (the singular). Empty for kinds not normalised this way.
func authoredType(kind string) string {
	switch kind {
	case "skills":
		return "skill"
	case "workflows":
		return "workflow"
	case "principles":
		return "principle"
	default:
		return ""
	}
}

// normalizeTypeDir back-fills/repairs the OKF `type` key for authored substrate
// (skills/workflows/principles) at ingest: it renames a legacy top-level `kind:`
// key to `type:` (value preserved) and inserts `type: <singular>` when neither is
// present, so every authored doc complies with OKF (`type` required) regardless
// of how it was authored. Idempotent; all other frontmatter is preserved
// untouched, testdata/ is skipped, and a frontmatter-less file is left for the
// structure check to flag.
func normalizeTypeDir(dir, typeVal string) {
	if strings.TrimSpace(dir) == "" || typeVal == "" {
		return
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if out, changed := normalizeType(string(body), typeVal); changed {
			_ = os.WriteFile(path, []byte(out), 0o644)
		}
		return nil
	})
}

// normalizeType rewrites a single authored doc's frontmatter so it carries the
// OKF `type` key: a legacy `kind:` is renamed to `type:` (value preserved), a
// redundant `kind:` alongside an existing `type:` is dropped, and a missing key
// is inserted. Returns changed=false when there is no frontmatter or nothing
// needed changing.
func normalizeType(body, typeVal string) (string, bool) {
	lines := strings.Split(body, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return body, false
	}
	end := -1
	for j := 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) == "---" {
			end = j
			break
		}
	}
	if end < 0 {
		return body, false
	}
	hasType := false
	for _, ln := range lines[1:end] {
		if strings.HasPrefix(strings.TrimSpace(ln), "type:") {
			hasType = true
			break
		}
	}
	var newFM []string
	changed := false
	for _, ln := range lines[1:end] {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "kind:") {
			if hasType {
				changed = true // drop the redundant legacy kind
				continue
			}
			newFM = append(newFM, "type: "+strings.TrimSpace(strings.TrimPrefix(t, "kind:")))
			hasType = true
			changed = true
			continue
		}
		newFM = append(newFM, ln)
	}
	if !hasType {
		newFM = append([]string{"type: " + typeVal}, newFM...)
		changed = true
	}
	if !changed {
		return body, false
	}
	var b strings.Builder
	b.WriteString("---\n")
	for _, ln := range newFM {
		b.WriteString(ln)
		b.WriteString("\n")
	}
	b.WriteString("---")
	if end+1 <= len(lines)-1 {
		b.WriteString("\n")
		b.WriteString(strings.Join(lines[end+1:], "\n"))
	}
	return b.String(), true
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

// okfEntry is one row of a bundle-root index.md: the link target (name), its
// display title, and an optional one-line description.
type okfEntry struct{ name, title, desc string }

// renderOKFIndex renders a bundle-root index.md body: the okf_version root plus a
// progressive-disclosure link list (name-sorted). It is the SINGLE index
// renderer, shared by the documents index writer and MaterializeOKF, so every
// OKF folder's index has one format. heading is the "# …" title (e.g. "Documents").
func renderOKFIndex(heading string, entries []okfEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	var b strings.Builder
	fmt.Fprintf(&b, "---\nokf_version: \"%s\"\n---\n\n# %s\n\n", okfVersion, heading)
	for _, e := range entries {
		if e.desc != "" && e.desc != e.title { // omit a description that just echoes the title
			fmt.Fprintf(&b, "* [%s](%s.md) - %s\n", e.title, e.name, e.desc)
		} else {
			fmt.Fprintf(&b, "* [%s](%s.md)\n", e.title, e.name)
		}
	}
	return b.String()
}

// writeIfChanged writes content to path only when it differs from what is there
// (so a regenerated file converges and does not churn). It creates the parent
// directory as needed. Returns whether it wrote.
func writeIfChanged(path, content string) (bool, error) {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(content), 0o644)
}

// writeOKFIndex regenerates the bundle-root index.md for the documents directory
// from the indexed concept documents — a progressive-disclosure link list with
// the root declaring okf_version. It is written only when its content changes
// (so it converges and does not churn the index), and is skipped entirely when
// there are no concepts and no existing index.
func (s *Store) writeOKFIndex(dir string, docs []Doc) error {
	var entries []okfEntry
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
		entries = append(entries, okfEntry{d.Name, title, fmScalar(fm, "description")})
	}
	// Progressive disclosure: surface each OKF sub-bundle (a subdir with its own
	// index.md) as ONE root entry linking its index, instead of its concept files.
	if subs, err := os.ReadDir(dir); err == nil {
		for _, de := range subs {
			if !de.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, de.Name(), "index.md")); err != nil {
				continue
			}
			entries = append(entries, okfEntry{de.Name() + "/index", de.Name(), "sub-bundle"})
		}
	}
	path := filepath.Join(dir, "index.md")
	if len(entries) == 0 {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil // nothing to list and no stale index to maintain
		}
	}
	content := renderOKFIndex("Documents", entries)
	_, err := writeIfChanged(path, content)
	return err
}

// summaryBundleDir is the documents sub-bundle that holds the per-story
// implementation (commit/push) summaries, so ~100 evidence docs live under one
// OKF sub-bundle instead of flooding the root documents index.
const summaryBundleDir = "story-implementation-summary"

// refreshSummaryBundle organises the per-story implementation summaries into the
// documents/story-implementation-summary/ OKF sub-bundle: it migrates any
// top-level commit-summary-*.md into the sub-bundle (idempotent) and regenerates
// the sub-bundle's reserved index.md/log.md from its concept files. Because the
// sub-bundle owns an index.md, walkMarkdown skips its subtree — so the summaries
// no longer flood the root documents list. Best-effort; called on the documents
// sync before the walk.
func refreshSummaryBundle(documentsDir string) {
	if strings.TrimSpace(documentsDir) == "" {
		return
	}
	sub := filepath.Join(documentsDir, summaryBundleDir)
	ents, err := os.ReadDir(documentsDir)
	if err != nil {
		return
	}
	haveSub := false
	for _, de := range ents {
		if de.IsDir() {
			if de.Name() == summaryBundleDir {
				haveSub = true
			}
			continue
		}
		if !strings.HasPrefix(de.Name(), "commit-summary") || !strings.HasSuffix(de.Name(), ".md") {
			continue
		}
		if err := os.MkdirAll(sub, 0o755); err != nil {
			return
		}
		haveSub = true
		src := filepath.Join(documentsDir, de.Name())
		if body, rerr := os.ReadFile(src); rerr == nil {
			if _, werr := writeIfChanged(filepath.Join(sub, de.Name()), string(body)); werr == nil {
				_ = os.Remove(src)
			}
		}
	}
	if haveSub {
		writeBundleIndexFromDir(sub, "Story implementation summaries")
	}
}

// writeBundleIndexFromDir regenerates a sub-bundle's reserved index.md and log.md
// from the concept files on disk (their frontmatter title/description/timestamp).
// Shares the one index renderer and log writer, so every OKF folder — root or
// sub-bundle — has the same format.
func writeBundleIndexFromDir(dir, heading string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var entries []okfEntry
	var items []OKFItem
	for _, de := range ents {
		if de.IsDir() || !strings.EqualFold(filepath.Ext(de.Name()), ".md") {
			continue
		}
		name := strings.TrimSuffix(de.Name(), filepath.Ext(de.Name()))
		if okfReserved(name) {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(dir, de.Name()))
		if rerr != nil {
			continue
		}
		fm, _, _ := splitFrontmatter(string(body))
		title := fmScalar(fm, "title")
		if title == "" {
			title = deriveTitle(string(body), name)
		}
		ts := parseOKFTime(fmScalar(fm, "timestamp"))
		if ts.IsZero() {
			if info, ierr := de.Info(); ierr == nil {
				ts = info.ModTime()
			}
		}
		entries = append(entries, okfEntry{name, title, fmScalar(fm, "description")})
		items = append(items, OKFItem{Name: name, Title: title, Timestamp: ts})
	}
	_, _ = writeIfChanged(filepath.Join(dir, "index.md"), renderOKFIndex(heading, entries))
	_ = okfWriteLog(dir, items)
}

// parseOKFTime parses an RFC3339 timestamp, returning the zero time on failure.
func parseOKFTime(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// okfGeneratedKey is the frontmatter marker every MaterializeOKF-written concept
// file carries. It flags the file as a regenerated read-only VIEW (the source of
// truth is the store, not the file) and is what prune targets — so a materialized
// folder that is also authored (e.g. documents) never has an authored file
// deleted, only stale generated ones.
const okfGeneratedKey = "generated"
const okfGeneratedVal = "satelle" // written as generated: satelle

// OKFItem is one record to materialize into an OKF folder as a read-only concept
// document. Name is the filename stem; Type is the required OKF type; Body is the
// markdown body (frontmatter is synthesized around it).
type OKFItem struct {
	Name        string
	Type        string
	Title       string
	Description string
	Body        string
	Tags        []string
	Timestamp   time.Time
}

// renderOKFItem renders one OKFItem to a full concept-document body: synthesized
// OKF frontmatter (required type, recommended fields, and the generated marker)
// followed by a "do not edit" banner and the item body.
func renderOKFItem(it OKFItem) string {
	desc := it.Description
	if desc == "" {
		desc = it.Title
	}
	typ := it.Type
	if typ == "" {
		typ = "document"
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "type: %s\n", yamlScalar(typ))
	fmt.Fprintf(&b, "title: %s\n", yamlScalar(it.Title))
	fmt.Fprintf(&b, "description: %s\n", yamlScalar(desc))
	tags := it.Tags
	if len(tags) == 0 {
		tags = []string{typ}
	}
	b.WriteString("tags:\n")
	for _, t := range tags {
		fmt.Fprintf(&b, "- %s\n", yamlScalar(t))
	}
	fmt.Fprintf(&b, "timestamp: %s\n", yamlScalar(it.Timestamp.UTC().Format(time.RFC3339)))
	fmt.Fprintf(&b, "%s: %s\n", okfGeneratedKey, okfGeneratedVal)
	b.WriteString("---\n\n")
	b.WriteString("<!-- generated by satelle — do not edit; the store is the source of truth, this file is a regenerated read-only view. -->\n\n")
	b.WriteString(strings.TrimLeft(it.Body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// isGenerated reports whether a file body carries the OKF generated marker (so it
// is a MaterializeOKF-written view file, safe to prune when its record is gone).
func isGenerated(body string) bool {
	fm, _, ok := splitFrontmatter(body)
	return ok && fmScalar(fm, okfGeneratedKey) != ""
}

// MaterializeOKF renders items into dir as a read-only OKF reference folder: one
// <Name>.md per item (via renderOKFItem), plus the reserved index.md (link list)
// and log.md (date-grouped changelog). It is the SINGLE materialization path for
// generated OKF surfaces (the story backlog, the summary sub-bundle). It is
// idempotent — each file is written only when its content changes — and it prunes
// stale generated files: a *generated* concept file (carrying the marker) whose
// name is not in items is deleted, while authored files and reserved files are
// left untouched. heading titles the index (e.g. "Backlog").
func MaterializeOKF(dir, heading string, items []OKFItem, now time.Time) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("docindex: MaterializeOKF: empty dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	want := make(map[string]struct{}, len(items))
	var entries []okfEntry
	for _, it := range items {
		if it.Name == "" || okfReserved(it.Name) {
			continue
		}
		want[it.Name] = struct{}{}
		path := filepath.Join(dir, it.Name+".md")
		if _, err := writeIfChanged(path, renderOKFItem(it)); err != nil {
			return err
		}
		desc := it.Description
		if desc == "" {
			desc = it.Title
		}
		title := it.Title
		if title == "" {
			title = it.Name
		}
		entries = append(entries, okfEntry{it.Name, title, desc})
	}

	// Prune stale generated files (never authored or reserved ones).
	ents, _ := os.ReadDir(dir)
	for _, de := range ents {
		if de.IsDir() || !strings.EqualFold(filepath.Ext(de.Name()), ".md") {
			continue
		}
		name := strings.TrimSuffix(de.Name(), filepath.Ext(de.Name()))
		if okfReserved(name) {
			continue
		}
		if _, keep := want[name]; keep {
			continue
		}
		p := filepath.Join(dir, de.Name())
		if body, err := os.ReadFile(p); err == nil && isGenerated(string(body)) {
			_ = os.Remove(p)
		}
	}

	if _, err := writeIfChanged(filepath.Join(dir, "index.md"), renderOKFIndex(heading, entries)); err != nil {
		return err
	}
	return okfWriteLog(dir, items)
}

// okfWriteLog regenerates the reserved log.md — a date-grouped changelog of the
// materialized items, newest date first, each entry a timestamped bullet. It
// gives an OKF folder its second reserved file so the bundle is complete.
func okfWriteLog(dir string, items []OKFItem) error {
	byDate := map[string][]OKFItem{}
	var dates []string
	for _, it := range items {
		if it.Name == "" || okfReserved(it.Name) {
			continue
		}
		day := it.Timestamp.UTC().Format("2006-01-02")
		if _, ok := byDate[day]; !ok {
			dates = append(dates, day)
		}
		byDate[day] = append(byDate[day], it)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	var b strings.Builder
	b.WriteString("# Log\n\n")
	for _, day := range dates {
		day := day
		list := byDate[day]
		sort.Slice(list, func(i, j int) bool { return list[i].Timestamp.After(list[j].Timestamp) })
		fmt.Fprintf(&b, "## %s\n\n", day)
		for _, it := range list {
			title := it.Title
			if title == "" {
				title = it.Name
			}
			fmt.Fprintf(&b, "* %s — [%s](%s.md)\n", it.Timestamp.UTC().Format("15:04"), title, it.Name)
		}
		b.WriteString("\n")
	}
	_, err := writeIfChanged(filepath.Join(dir, "log.md"), b.String())
	return err
}
