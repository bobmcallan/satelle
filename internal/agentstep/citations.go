package agentstep

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/bobmcallan/satelle/internal/docindex"
)

// payloadCitationsKey is the reviewer-skill frontmatter key that opts a gate into
// citation excerpts: its value is the type of the document whose file:line
// citations are resolved (`payload_citations: plan`). A rubric without it gets no
// block, so the mechanism names no skill and no status.
const payloadCitationsKey = "payload_citations"

// The excerpt block is bounded on its own, independent of docsPayloadCeiling, so
// a plan that cites forty files cannot starve the plan the reviewer must judge.
const (
	// citationContext is the lines of context either side of a cited span.
	citationContext = 3
	// citationSpanCeiling caps one cited span; a longer one is cut and flagged.
	citationSpanCeiling = 40
	// citationCount bounds how many citations carry an excerpt.
	citationCount = 60
	// CitationsPayloadCeiling bounds the excerpt bytes across the block. It is
	// exported because validate's budget report adds it to a declaring seat's row.
	CitationsPayloadCeiling = 24 << 10
	// citationListCeiling bounds how many citations are listed at all; the rest
	// are only counted in Omitted.
	citationListCeiling = 400
	// citationFileCeiling is the largest file read for an excerpt.
	citationFileCeiling = 2 << 20
)

// CitationsState is the resolved citation block of a plan-review payload.
type CitationsState struct {
	Citations []Citation `json:"citations"`
	// Truncated is set when any citation lost its excerpt to a bound; Omitted
	// counts them, including any not listed past citationListCeiling.
	Truncated bool `json:"truncated,omitempty"`
	Omitted   int  `json:"omitted,omitempty"`
	// Tree is the judged tree the lines were read from.
	Tree string `json:"tree,omitempty"`
}

// Citation is one path:line or path:start-end citation, in plan order. Exactly
// one of Excerpt, Unresolved and Omitted describes it.
type Citation struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	// Excerpt is the cited lines plus citationContext lines either side, each
	// prefixed with its line number; ContextStart/ContextEnd say which.
	Excerpt      string `json:"excerpt,omitempty"`
	ContextStart int    `json:"context_start,omitempty"`
	ContextEnd   int    `json:"context_end,omitempty"`
	// Truncated marks a span cut at citationSpanCeiling lines.
	Truncated  bool   `json:"truncated,omitempty"`
	Unresolved bool   `json:"unresolved,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Omitted marks a citation listed without its excerpt because the block hit
	// its count or byte bound; the file is still there to open.
	Omitted bool `json:"omitted,omitempty"`
}

// citationRE matches path:N and path:N-M. The character before the path is not a
// path character or a colon, so the tail of a URL (`host:8080/a.go:5`) is not a
// citation; the digits are greedy so the next character is never a digit.
var citationRE = regexp.MustCompile(`(?:^|[^A-Za-z0-9_./\-:])([A-Za-z0-9_./\-]+):(\d+)(?:-(\d+))?`)

// parseCitations lists the distinct file:line citations in body, in order. A path
// must look like a file — it contains a slash or ends in an extension that starts
// with a letter — so `step:2` and `12:30` are not citations.
func parseCitations(body string) []Citation {
	var out []Citation
	seen := map[Citation]bool{}
	for _, m := range citationRE.FindAllStringSubmatch(body, -1) {
		path := strings.TrimRight(m[1], ".")
		if !fileLike(path) {
			continue
		}
		start, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		end := start
		if m[3] != "" {
			if end, err = strconv.Atoi(m[3]); err != nil {
				continue
			}
		}
		c := Citation{Path: path, Start: start, End: end}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func fileLike(path string) bool {
	if path == "" || strings.HasPrefix(path, "http") {
		return false
	}
	if strings.Contains(path, "/") {
		return true
	}
	ext := filepath.Ext(path)
	return len(ext) > 1 && unicode.IsLetter(rune(ext[1]))
}

// citationDocType is the document type a rubric opts into, or "".
func citationDocType(rubric string) string {
	lines, ok := docindex.Frontmatter(rubric)
	if !ok {
		return ""
	}
	prefix := payloadCitationsKey + ":"
	for _, l := range lines {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, prefix) {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(t, prefix)), `"'`)
		}
	}
	return ""
}

// DeclaresPayloadCitations reports whether a reviewer rubric opts into citation
// excerpts, so the injected-size report can account for the block.
func DeclaresPayloadCitations(rubric string) bool {
	return citationDocType(rubric) != ""
}

// bundleCitations is the citation block a bundled session is handed: resolved
// once, from whichever rubric of the bundle opted in. Nil when none did.
func bundleCitations(preps []reviewerPrep) (tp, mat *CitationsState) {
	for _, p := range preps {
		if p.tp.Citations != nil {
			return p.tp.Citations, p.material.Citations
		}
	}
	return nil, nil
}

// fillCitations resolves the citations of the document type the rubric names to
// the lines they cite in the judged tree. Gate payload only, and only for a rubric
// that opts in; it never fails the transition — a citation it cannot read is
// listed as unresolved with the reason.
func (g *Engine) fillCitations(ctx context.Context, itemID, rubric string, tp *transitionPayload, mat *reviewerMaterial) {
	if mat == nil || itemID == "" {
		return
	}
	docType := citationDocType(rubric)
	if docType == "" {
		return
	}
	var bodies []string
	for _, d := range mat.Docs {
		if !d.Binary && strings.EqualFold(d.Type, docType) {
			bodies = append(bodies, d.Body)
		}
	}
	cites := parseCitations(strings.Join(bodies, "\n"))
	if len(cites) == 0 {
		return
	}
	var tree string
	var terr error
	if g.citationTree == nil {
		terr = fmt.Errorf("no tree resolver wired")
	} else {
		tree, terr = g.citationTree(ctx, itemID)
	}
	st := resolveCitations(tree, terr, cites)
	tp.Citations, mat.Citations = st, st
}

// resolveCitations reads each citation from tree under the count and byte
// bounds. Every citation is listed, in order: one that cannot be read carries its
// reason, and one past a bound carries Omitted rather than disappearing.
func resolveCitations(tree string, treeErr error, cites []Citation) *CitationsState {
	st := &CitationsState{Citations: make([]Citation, 0, min(len(cites), citationListCeiling))}
	if treeErr == nil {
		st.Tree = tree
	}
	files := map[string]*citedFile{}
	excerpts, used := 0, 0
	for i, c := range cites {
		if i >= citationListCeiling {
			st.Omitted += len(cites) - i
			st.Truncated = true
			break
		}
		switch {
		case treeErr != nil:
			c.Unresolved, c.Reason = true, "judged tree unavailable: "+treeErr.Error()
		default:
			f, ok := files[c.Path]
			if !ok {
				f = readCitedFile(tree, c.Path)
				files[c.Path] = f
			}
			c = f.excerpt(c)
		}
		if c.Excerpt != "" && (excerpts >= citationCount || used+len(c.Excerpt) > CitationsPayloadCeiling) {
			c = Citation{Path: c.Path, Start: c.Start, End: c.End, Omitted: true}
		}
		switch {
		case c.Omitted:
			st.Omitted++
			st.Truncated = true
		case c.Excerpt != "":
			excerpts++
			used += len(c.Excerpt)
		}
		st.Citations = append(st.Citations, c)
	}
	return st
}

// citedFile is one file read once for every citation of it: its lines, or why it
// could not be read.
type citedFile struct {
	lines  []string
	reason string
}

func readCitedFile(tree, path string) *citedFile {
	root, err := filepath.EvalSymlinks(tree)
	if err != nil {
		return &citedFile{reason: "judged tree unavailable: " + err.Error()}
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, path)
	}
	if !within(root, full) {
		return &citedFile{reason: "outside the judged tree"}
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return &citedFile{reason: "file not found"}
	}
	if !within(root, real) {
		return &citedFile{reason: "outside the judged tree"}
	}
	fi, err := os.Stat(real)
	if err != nil || fi.IsDir() {
		return &citedFile{reason: "file not found"}
	}
	if fi.Size() > citationFileCeiling {
		return &citedFile{reason: fmt.Sprintf("file larger than %d bytes", citationFileCeiling)}
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return &citedFile{reason: "file not found"}
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	return &citedFile{lines: strings.Split(string(b), "\n")}
}

// within reports whether path is root or lies beneath it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// excerpt fills c from the file: the cited span, clamped to the file and cut at
// citationSpanCeiling lines, with citationContext lines either side.
func (f *citedFile) excerpt(c Citation) Citation {
	unresolved := func(reason string) Citation {
		return Citation{Path: c.Path, Start: c.Start, End: c.End, Unresolved: true, Reason: reason}
	}
	if f.reason != "" {
		return unresolved(f.reason)
	}
	if c.Start < 1 || c.End < c.Start {
		return unresolved("invalid span")
	}
	total := len(f.lines)
	if c.Start > total {
		return unresolved(fmt.Sprintf("line %d beyond end of file (%d lines)", c.Start, total))
	}
	end := min(c.End, total)
	if end-c.Start+1 > citationSpanCeiling {
		end = c.Start + citationSpanCeiling - 1
		c.Truncated = true
	}
	c.ContextStart = max(c.Start-citationContext, 1)
	c.ContextEnd = min(end+citationContext, total)
	var sb strings.Builder
	for n := c.ContextStart; n <= c.ContextEnd; n++ {
		fmt.Fprintf(&sb, "%d: %s\n", n, f.lines[n-1])
	}
	c.Excerpt = sb.String()
	return c
}
