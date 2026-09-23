package compact

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/bobmcallan/satelle/internal/retrieve"
)

// CompactPatch compacts a unified diff patch in the shape `satelle story diff
// --patch` produces: one or more `diff --git a/X b/Y` file sections, each
// carrying an `index abc..def` line, `---`/`+++` headers, and one or more
// `@@ -a,b +c,d @@` hunks.
//
// It always drops the `index` line — the one deliberately lossy-without-
// offload element this story sanctions (there is nothing to retrieve back; a
// content hash on its own carries no reviewable meaning). A hunk that is
// whitespace-only (every removed/added line pairs up equal once whitespace is
// stripped) is replaced by a single offloaded marker line, and so is an
// entire file section whose path matches a glob in noise (basename match
// unless the glob contains "/", mirroring [gate] edit_exempt_globs
// semantics). Either replacement applies only when the marker line is
// actually shorter than what it replaces (AC3) — with no offloader, or when
// offloading would not shrink the section, the original hunk text stays.
//
// off nil-safe: with no offloader, no hunk or section is ever replaced — the
// index-line drop is the only compaction still applied.
func CompactPatch(patch string, noise []string, off Offloader) string {
	if patch == "" {
		return patch
	}
	var out strings.Builder
	for _, sec := range splitFileSections(patch) {
		out.WriteString(compactSection(sec, noise, off))
	}
	return out.String()
}

var diffGitRE = regexp.MustCompile(`^diff --git a/(.*) b/(.*)\r?\n$`)

// splitFileSections splits patch into per-file sections, each starting at (and
// including) its `diff --git ` line. A patch with no such line is one section.
func splitFileSections(patch string) []string {
	lines := splitKeepEnds(patch)
	var sections []string
	var cur strings.Builder
	started := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "diff --git ") {
			if started {
				sections = append(sections, cur.String())
				cur.Reset()
			}
			started = true
		}
		cur.WriteString(ln)
	}
	if cur.Len() > 0 {
		sections = append(sections, cur.String())
	}
	if !started {
		return []string{patch}
	}
	return sections
}

func compactSection(sec string, noise []string, off Offloader) string {
	lines := splitKeepEnds(sec)
	if len(lines) == 0 {
		return sec
	}

	i := 0
	var head []string
	for i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") {
		if strings.HasPrefix(lines[i], "index ") {
			i++
			continue
		}
		head = append(head, lines[i])
		i++
	}
	hunkLines := lines[i:]
	filePath := parseDiffGitPath(head)

	if off != nil && matchesAny(filePath, noise) && len(hunkLines) > 0 {
		body := strings.Join(hunkLines, "")
		if marker, ok := maybeOffloadHunk(body, "hunk", off); ok {
			return strings.Join(head, "") + marker
		}
	}

	var out []string
	out = append(out, head...)
	for _, h := range splitHunks(hunkLines) {
		body := strings.Join(h, "")
		if off != nil && isWhitespaceOnlyHunk(h) {
			if marker, ok := maybeOffloadHunk(body, "hunk", off); ok {
				out = append(out, marker)
				continue
			}
		}
		out = append(out, h...)
	}
	return strings.Join(out, "")
}

// maybeOffloadHunk stores body and returns the replacement marker line, only
// when that line is shorter than body (AC3 — never grow the output).
func maybeOffloadHunk(body, kind string, off Offloader) (string, bool) {
	if body == "" {
		return "", false
	}
	n := strings.Count(body, "\n")
	hash, err := off.Put([]byte(body))
	if err != nil {
		return "", false
	}
	marker := "@@ offloaded " + retrieve.MarkerKind(hash, kind, len(body)) + " (" + strconv.Itoa(n) + " lines) @@\n"
	if len(marker) >= len(body) {
		return "", false
	}
	return marker, true
}

// splitHunks groups hunkLines (everything from the first "@@ " line to the end
// of a file section) into individual hunks, each starting at its "@@ " header.
func splitHunks(lines []string) [][]string {
	var hunks [][]string
	var cur []string
	for _, ln := range lines {
		if strings.HasPrefix(ln, "@@ ") {
			if len(cur) > 0 {
				hunks = append(hunks, cur)
			}
			cur = nil
		}
		cur = append(cur, ln)
	}
	if len(cur) > 0 {
		hunks = append(hunks, cur)
	}
	return hunks
}

// isWhitespaceOnlyHunk reports whether every removed line pairs, in order,
// with an added line equal once whitespace is stripped. h[0] is the "@@ "
// header line.
func isWhitespaceOnlyHunk(h []string) bool {
	if len(h) < 2 {
		return false
	}
	var removed, added []string
	for _, ln := range h[1:] {
		switch {
		case strings.HasPrefix(ln, "-"):
			removed = append(removed, stripWS(strings.TrimPrefix(ln, "-")))
		case strings.HasPrefix(ln, "+"):
			added = append(added, stripWS(strings.TrimPrefix(ln, "+")))
		}
	}
	if len(removed) == 0 || len(removed) != len(added) {
		return false
	}
	for i := range removed {
		if removed[i] != added[i] {
			return false
		}
	}
	return true
}

func stripWS(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// parseDiffGitPath derives the section's file path from its head lines
// (everything up to the first hunk). gitDiffSince forces a/ b/ headers so this
// repo's own diffs always take the fast path below, but a patch pasted in
// from elsewhere may come off a machine with diff.mnemonicPrefix (c/ w/) or
// diff.noprefix (no prefix at all) set. The "diff --git X Y" line is
// ambiguous to split generically when a path contains a space, so the
// fallback reads the unambiguous ---/+++ lines instead: each carries exactly
// one path, under whatever prefix that machine used.
func parseDiffGitPath(head []string) string {
	if len(head) == 0 {
		return ""
	}
	if m := diffGitRE.FindStringSubmatch(head[0]); m != nil {
		if m[2] != "" && m[2] != "dev/null" {
			return m[2]
		}
		return m[1]
	}
	var minus string
	for _, ln := range head {
		switch {
		case strings.HasPrefix(ln, "+++ "):
			if p := parsePathLine(ln, "+++ "); p != "" {
				return p
			}
		case strings.HasPrefix(ln, "--- "):
			minus = parsePathLine(ln, "--- ")
		}
	}
	return minus
}

// parsePathLine strips prefix and any trailing "\t..." git appends after a
// quoted/escaped path, then strips a leading diff prefix segment if present.
func parsePathLine(ln, prefix string) string {
	p := strings.TrimPrefix(ln, prefix)
	p = strings.TrimRight(p, "\r\n")
	if idx := strings.IndexByte(p, '\t'); idx >= 0 {
		p = p[:idx]
	}
	if p == "" || p == "/dev/null" {
		return ""
	}
	return stripDiffPrefix(p)
}

// diffPrefixLetters are the single-character prefixes git itself ever puts in
// front of a diff path: a/ b/ (default), and the mnemonicPrefix set c/
// (commit) i/ (index) o/ (object) w/ (working tree). Stripping is gated on
// this whitelist — not "any leading path segment" — because under
// diff.noprefix a real nested path (e.g. "internal/foo/foo.go") has no
// prefix at all, and blindly cutting its first segment would eat a real
// directory instead of a prefix.
var diffPrefixLetters = map[byte]bool{'a': true, 'b': true, 'c': true, 'i': true, 'o': true, 'w': true}

func stripDiffPrefix(p string) string {
	idx := strings.IndexByte(p, '/')
	if idx != 1 || !diffPrefixLetters[p[0]] {
		return p
	}
	return p[idx+1:]
}

// matchesAny reports whether p matches any glob in globs. A glob containing
// "/" matches the full path; otherwise it matches only p's basename — the
// same split [gate] edit_exempt_globs uses.
func matchesAny(p string, globs []string) bool {
	if p == "" {
		return false
	}
	base := path.Base(p)
	for _, g := range globs {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if strings.Contains(g, "/") {
			if ok, _ := path.Match(g, p); ok {
				return true
			}
			continue
		}
		if ok, _ := path.Match(g, base); ok {
			return true
		}
	}
	return false
}

func splitKeepEnds(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.SplitAfter(s, "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
